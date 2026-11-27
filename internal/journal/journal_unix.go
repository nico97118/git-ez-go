//go:build linux || freebsd || darwin

// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package journal publishes atomic intervention evidence and keeps
// transient engine safety state separate from persistent LLM audit records.
package journal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nico97118/git-ez-go/internal/failure"
)

type Journal struct {
	Directory         string
	View              map[string]any
	root              *os.Root
	secrets           []string
	common, workspace string
	safety            *os.Root
	llm               bool
	scratch           bool
	directories       []directoryIdentity
}

type directoryIdentity struct {
	path string
	info os.FileInfo
}

func (d directoryIdentity) check() error {
	actual, err := os.Lstat(d.path)
	if err != nil || !actual.IsDir() || !os.SameFile(actual, d.info) {
		return failure.New(failure.Audit, "Journal directory was replaced: "+d.path)
	}
	return nil
}

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func private(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == os.Geteuid() && info.IsDir() && info.Mode().Perm()&0077 == 0
}
func New(common string, view map[string]any, secrets ...string) (*Journal, error) {
	return newRun(common, view, false, secrets...)
}
func openRunDirectory(common string) (*Journal, error) {
	root, e := os.OpenRoot(common)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	parent := root
	var opened []*os.Root
	var identities []directoryIdentity
	defer func() {
		for _, r := range opened {
			_ = r.Close()
		}
	}()
	directory := common
	for depth, name := range []string{"ez", "runs", ID()} {
		if e = parent.Mkdir(name, 0700); e != nil && (depth == 2 || !os.IsExist(e)) {
			return nil, failure.Wrap(failure.Audit, "Cannot create journal directory: "+filepath.Join(directory, name), e)
		}
		info, err := parent.Lstat(name)
		if err != nil || !info.IsDir() {
			return nil, failure.New(failure.Audit, "Journal path must be a real directory: "+filepath.Join(directory, name))
		}
		next, err := parent.OpenRoot(name)
		if err != nil {
			return nil, err
		}
		heldInfo, statErr := next.Stat(".")
		if statErr != nil || !os.SameFile(info, heldInfo) {
			next.Close()
			return nil, failure.New(failure.Audit, "Journal directory was replaced while opening")
		}
		opened = append(opened, next)
		parent = next
		directory = filepath.Join(directory, name)
		identities = append(identities, directoryIdentity{directory, info})
	}
	// Keep an independent directory handle through resolver execution/finalization.
	held, e := parent.OpenRoot(".")
	if e != nil {
		return nil, e
	}
	return &Journal{Directory: directory, root: held, directories: identities}, nil
}

func newRun(common string, view map[string]any, llm bool, secrets ...string) (*Journal, error) {
	j, e := openRunDirectory(common)
	if e != nil {
		return nil, e
	}
	j.View, j.secrets, j.llm = view, append([]string{}, secrets...), llm

	view["id"] = filepath.Base(j.Directory)
	view["schema_version"] = 4
	view["created_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	view["status"] = "running"
	view["integrated"] = false
	if e = j.Save(); e != nil {
		_ = j.root.Close()
		return nil, e
	}
	return j, nil
}
func (j *Journal) Close() error {
	owned := true
	if j.workspace != "" {
		held := j.root
		if j.safety != nil {
			held = j.safety
		}
		actual, e := os.Lstat(j.workspace)
		original, he := held.Stat(".")
		owned = e == nil && he == nil && private(actual) && os.SameFile(actual, original)
	}
	err := j.root.Close()
	if j.safety != nil {
		err = errors.Join(err, j.safety.Close())
	}
	if j.workspace != "" {
		if owned {
			err = errors.Join(err, os.RemoveAll(j.workspace))
		} else {
			err = errors.Join(err, failure.New(failure.Cleanup, "Engine workspace was replaced; cleanup refused"))
		}
	}
	return err
}
func (j *Journal) Redact(b []byte) []byte {
	for _, s := range j.secrets {
		if s != "" {
			b = bytes.ReplaceAll(b, []byte(s), []byte("<redacted>"))
		}
	}
	return b
}
func (j *Journal) Check() error {
	for _, d := range j.directories {
		if err := d.check(); err != nil {
			return err
		}
	}
	if j.safety != nil {
		info, e := os.Lstat(j.workspace)
		held, he := j.safety.Stat(".")
		if e != nil || he != nil || !private(info) || !os.SameFile(info, held) {
			return failure.New(failure.Audit, "Engine safety workspace was replaced")
		}
	}
	actual, e := os.Lstat(j.Directory)
	if e != nil {
		return failure.Wrap(failure.Audit, "Journal directory was replaced", e)
	}
	held, e := j.root.Stat(".")
	if e != nil || !actual.IsDir() || (j.scratch && !private(actual)) || !os.SameFile(actual, held) {
		return failure.New(failure.Audit, "Journal directory was replaced")
	}
	return nil
}
func (j *Journal) Write(name string, b []byte) error { return j.publish(name, j.Redact(b)) }
func (j *Journal) publish(name string, b []byte) error {
	if filepath.Base(name) != name || name == "." || strings.ContainsRune(name, 0) {
		return failure.New(failure.Audit, "Invalid journal artifact name")
	}
	if e := j.Check(); e != nil {
		return e
	}
	nameTmp := ".publish-" + ID()
	f, e := j.root.OpenFile(nameTmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return failure.Wrap(failure.Audit, "Cannot create journal artifact", e)
	}
	defer j.root.Remove(nameTmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = j.root.Rename(nameTmp, name)
	}
	if e == nil {
		d, err := j.root.Open(".")
		if err == nil {
			e = d.Sync()
			_ = d.Close()
		} else {
			e = err
		}
	}
	if e != nil {
		return failure.Wrap(failure.Audit, "Cannot publish required journal evidence", e)
	}
	return nil
}
func (j *Journal) JSON(name string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return failure.Wrap(failure.Audit, "Cannot encode required journal evidence", e)
	}
	// Redact values before encoding again so credentials containing quotes or
	// backslashes cannot evade JSON-level string replacement.
	var values any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(&values); e != nil {
		return e
	}
	values = j.redactValue(values)
	b, e = json.MarshalIndent(values, "", "  ")
	if e != nil {
		return e
	}
	return j.publish(name, append(b, '\n'))
}
func (j *Journal) redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return string(j.Redact([]byte(x)))
	case []any:
		for i := range x {
			x[i] = j.redactValue(x[i])
		}
	case map[string]any:
		for k, y := range x {
			x[k] = j.redactValue(y)
		}
	}
	return v
}
func (j *Journal) Save() error {
	if j.scratch {
		return j.JSON("engine-state.json", map[string]any{"scope": "engine_safety", "state": j.View})
	}
	if !j.llm {
		return j.JSON("run.json", j.View)
	}
	// Persist only the intervention view. Orchestration diagnostics stay transient.
	keep := []string{"id", "schema_version", "created_at", "finished_at", "status", "integrated", "error", "operation", "branch", "worktree", "target", "before_oid", "target_oid", "target_ref", "onto_oid", "result_oid", "provider", "model", "min_confidence", "interactive", "conflicts", "reviews", "sessions", "validation", "audit_errors"}
	view := map[string]any{"audit_scope": "llm_resolution"}
	for _, key := range keep {
		if value, ok := j.View[key]; ok {
			view[key] = value
		}
	}
	if items, ok := j.View["conflicts"].([]any); ok {
		filtered := []any{}
		for _, item := range items {
			old := item.(map[string]any)
			record := map[string]any{}
			for k, v := range old {
				if k != "artifacts" && k != "resolution" {
					record[k] = v
				}
			}
			filtered = append(filtered, record)
		}
		view["conflicts"] = filtered
	}
	return j.JSON("run.json", view)
}
func (j *Journal) Finish(err error) error {
	if err == nil {
		j.View["status"] = "succeeded"
	} else {
		j.View["status"] = "failed"
		if errors.Is(err, context.Canceled) {
			j.View["status"] = "interrupted"
		}
		j.View["error"] = string(j.Redact([]byte(err.Error())))
	}
	j.View["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return j.Save()
}

// Read is bounded and refuses a symlink/FIFO/nonregular final component.
func Read(path string, limit int64) ([]byte, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > limit || limit < 0 {
		return nil, fmt.Errorf("Evidence must be bounded regular data")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("Evidence exceeds capture limit")
	}
	return b, e
}

// Session opens a fresh private evidence directory under an admitted engine run.
// It reuses the same rooted publication and redaction policy; it is not a new run.
func Session(parent string, secrets ...string) (*Journal, error) {
	info, e := os.Lstat(parent)
	if e != nil || !info.IsDir() {
		return nil, failure.New(failure.Audit, "Agent evidence parent must be a real engine journal")
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	heldInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(info, heldInfo) {
		return nil, failure.New(failure.Audit, "Agent evidence parent was replaced")
	}
	name := "agent-" + ID()
	if e = root.Mkdir(name, 0700); e != nil {
		return nil, e
	}
	child, e := root.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	return &Journal{Directory: filepath.Join(parent, name), View: map[string]any{}, root: child, secrets: append([]string{}, secrets...), directories: []directoryIdentity{{parent, info}}}, nil
}

// Window reads a bounded portion through the held private directory handle.
func (j *Journal) Window(name string, offset, length int64) ([]byte, error) {
	if filepath.Base(name) != name || offset < 0 || length < 0 {
		return nil, failure.New(failure.Audit, "Invalid artifact window")
	}
	if e := j.Check(); e != nil {
		return nil, e
	}
	f, e := j.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, failure.New(failure.Audit, "Artifact must be regular data")
	}
	if offset > info.Size() {
		return nil, failure.New(failure.Audit, "Artifact offset exceeds retained bytes")
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return nil, e
	}
	return io.ReadAll(io.LimitReader(f, length))
}

// FilterJSON preserves JSON types and integer precision while filtering values.
func FilterJSON(value any, secrets ...string) ([]byte, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var decoded any
	if e = decoder.Decode(&decoded); e != nil {
		return nil, e
	}
	j := &Journal{secrets: secrets}
	return json.Marshal(j.redactValue(decoded))
}

// JSONEvidence publishes complete type-preserving credential-filtered evidence.
// Model payloads already reside in the framework/provider's in-memory JSON API.
func (j *Journal) JSONEvidence(name string, value any) (int, error) {
	b, e := FilterJSON(value, j.secrets...)
	if e != nil {
		return 0, e
	}
	b = append(b, '\n')
	if e = j.publish(name, b); e != nil {
		return 0, e
	}
	return len(b), nil
}

// RawEvidence streams complete raw payloads without another full redacted copy.
func (j *Journal) RawEvidence(name string, b []byte) (int, error) {
	c, e := j.Capture(name)
	if e != nil {
		return 0, e
	}
	_, writeErr := c.Write(b)
	if e = errors.Join(writeErr, c.Close()); e != nil {
		return 0, e
	}
	return int(c.Bytes), nil
}

// Child creates an engine-owned private evidence directory with rooted atomic
// publication and the same credential filtering as its parent.
func (j *Journal) Child(name string) (*Journal, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, failure.New(failure.Audit, "Invalid evidence directory")
	}
	if e := j.Check(); e != nil {
		return nil, e
	}
	if e := j.root.Mkdir(name, 0700); e != nil {
		return nil, e
	}
	root, e := j.root.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	return &Journal{Directory: filepath.Join(j.Directory, name), View: map[string]any{}, root: root, secrets: append([]string{}, j.secrets...), directories: append([]directoryIdentity{}, j.directories...)}, nil
}

// Preflight verifies audit publication before native continuation changes Git state.
func Preflight(common string) error {
	_, err := prepareStorage(common)
	return err
}

func prepareStorage(common string) ([]directoryIdentity, error) {
	probe, e := openRunDirectory(common)
	if e != nil {
		return nil, e
	}
	probeErr := probe.Write("probe", []byte("git-ez audit storage probe\n"))
	identities := append([]directoryIdentity{}, probe.directories[:len(probe.directories)-1]...)
	cleanupErr := probe.root.Remove("probe")
	removeErr := probe.Check()
	if removeErr == nil {
		removeErr = os.Remove(probe.Directory)
	}
	closeErr := probe.Close()
	if e = errors.Join(probeErr, cleanupErr, closeErr, removeErr); e != nil {
		return nil, failure.Wrap(failure.Audit, "Cannot prepare journal storage", e)
	}
	return identities, nil
}

// Scratch creates mandatory private engine state without creating a persistent
// LLM audit. Native-only operations remove this workspace on completion.
func Scratch(common string, view map[string]any, secrets ...string) (*Journal, error) {
	identities, e := prepareStorage(common)
	if e != nil {
		return nil, e
	}
	directory, e := os.MkdirTemp("", "git-ez-engine-")
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		os.RemoveAll(directory)
		return nil, e
	}
	j := &Journal{Directory: directory, View: view, root: root, secrets: append([]string{}, secrets...), common: common, workspace: directory, scratch: true, directories: identities}
	view["id"] = ID()
	view["schema_version"] = 4
	view["created_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	view["status"] = "running"
	view["integrated"] = false
	if e = j.Save(); e != nil {
		j.Close()
		return nil, e
	}
	return j, nil
}

// Promote publishes the persistent intervention before any resolver invocation.
// Replay captures keep their original transient paths and never become artifacts.
func (j *Journal) Promote() error {
	if !j.scratch {
		return nil
	}
	if e := j.Check(); e != nil {
		return e
	}
	next, e := newRun(j.common, j.View, true, j.secrets...)
	if e != nil {
		return e
	}
	j.directories = next.directories
	j.safety = j.root
	j.root = next.root
	j.Directory = next.Directory
	j.llm = true
	j.scratch = false
	return nil
}
func (j *Journal) Persistent() bool { return j.llm }
