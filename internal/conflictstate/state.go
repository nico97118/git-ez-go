// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package conflictstate preserves a native conflict stop, without stashes or
// traversal/removal of nontracked worktree data. Backups are private and transient.
package conflictstate

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/process"
)

type saved struct {
	Mode                os.FileMode
	OID, Link, Artifact string
	Modified, Accessed  time.Time
	Missing             bool
}
type State struct {
	Directory               string
	g                       git.Client
	files                   map[string]saved
	index, shared, metadata string
	entries                 []byte
	controls                map[string]string
	sha256                  bool
}

var controlNames = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "MERGE_AUTOSTASH", "AUTO_MERGE", "REBASE_HEAD", "CHERRY_PICK_HEAD", "sequencer", "index.lock"}

func New(ctx context.Context, g git.Client) (state *State, err error) {
	dir, err := os.MkdirTemp("", "git-ez-conflict-")
	if err != nil {
		return nil, err
	}
	s := &State{Directory: dir, g: g, files: map[string]saved{}, controls: map[string]string{}}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	format, err := g.Text(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	s.sha256 = format == "sha256"
	entries, err := g.Checked(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	s.entries = append([]byte{}, entries.Stdout...)
	indexed := map[string]string{}
	for _, entry := range bytes.Split(s.entries, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(entry, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("Invalid index entry")
		}
		if fields[0] != "100644" && fields[0] != "100755" && fields[0] != "120000" {
			return nil, fmt.Errorf("Unsupported tracked file in interactive snapshot: %s", name)
		}
		oid := ""
		if fields[2] == "0" {
			oid = fields[1]
		}
		indexed[string(name)] = oid
	}
	for name, indexedOID := range indexed {
		path, e := s.path(name)
		if e != nil {
			return nil, e
		}
		value, e := s.inspect(path)
		if e != nil {
			return nil, e
		}
		if !value.Missing && value.Mode.IsRegular() && value.OID != indexedOID {
			value.Artifact = filepath.Join(dir, "worktree", filepath.FromSlash(name))
			if e = copyFile(path, value.Artifact, value.Mode); e != nil {
				return nil, e
			}
		}
		s.files[name] = value
	}
	s.index, err = g.Path(ctx, "index")
	if err != nil {
		return nil, err
	}
	if err = copyFileMode(s.index, filepath.Join(dir, "index")); err != nil {
		return nil, err
	}
	shared, err := g.Text(ctx, "rev-parse", "--shared-index-path")
	if err != nil {
		return nil, err
	}
	if shared != "" {
		if !filepath.IsAbs(shared) {
			shared = filepath.Join(g.Dir, shared)
		}
		s.shared = shared
		if err = copyFileMode(shared, filepath.Join(dir, "shared-index")); err != nil {
			return nil, err
		}
	}
	s.metadata, err = g.Text(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	for _, name := range controlNames {
		source := filepath.Join(s.metadata, name)
		if err = copyNode(source, filepath.Join(dir, "control", name)); err != nil {
			return nil, err
		}
		digest, e := nodeDigest(source)
		if e != nil {
			return nil, e
		}
		s.controls[name] = digest
	}
	return s, nil
}
func (s *State) path(name string) (string, error) {
	if strings.EqualFold(filepath.Base(name), ".git") {
		return "", fmt.Errorf("Unsafe tracked path")
	}
	parent, e := s.g.SafePath(filepath.ToSlash(filepath.Dir(name)))
	if e != nil {
		return "", e
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}
func (s *State) inspect(path string) (saved, error) {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return saved{Missing: true}, nil
	}
	if e != nil {
		return saved{}, e
	}
	v := saved{Mode: info.Mode(), Modified: info.ModTime(), Accessed: accessTime(info)}
	if info.Mode()&os.ModeSymlink != 0 {
		v.Link, e = os.Readlink(path)
		return v, e
	}
	if !info.Mode().IsRegular() {
		return v, fmt.Errorf("Nonregular tracked file blocks snapshot or restoration: %s", path)
	}
	f, e := openRegular(path)
	if e != nil {
		return v, e
	}
	defer f.Close()
	var digest hash.Hash = sha1.New()
	if s.sha256 {
		digest = sha256.New()
	}
	fmt.Fprintf(digest, "blob %d%c", info.Size(), 0)
	if _, e = io.Copy(digest, f); e != nil {
		return v, e
	}
	v.OID = hex.EncodeToString(digest.Sum(nil))
	return v, nil
}
func equal(a, b saved) bool {
	return a.Missing == b.Missing && a.Mode == b.Mode && a.OID == b.OID && a.Link == b.Link
}
func (s *State) Changed() ([]string, error) {
	out := []string{}
	for name, old := range s.files {
		path, e := s.path(name)
		if e != nil {
			return nil, e
		}
		now, e := s.inspect(path)
		if e != nil {
			return nil, e
		}
		if !equal(old, now) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (s *State) VerifyControl() error {
	for _, name := range controlNames {
		digest, e := nodeDigest(filepath.Join(s.metadata, name))
		if e != nil {
			return e
		}
		if digest != s.controls[name] {
			return fmt.Errorf("Git operation metadata changed before approval: %s", name)
		}
	}
	return nil
}
func (s *State) Restore(ctx context.Context) error {
	names := make([]string, 0, len(s.files))
	for n := range s.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		old := s.files[name]
		path, e := s.path(name)
		if e != nil {
			return e
		}
		now, e := s.inspect(path)
		if e == nil && equal(old, now) {
			continue
		}
		info, statErr := os.Lstat(path)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if info != nil && info.IsDir() {
			if e = os.Remove(path); e != nil {
				return e
			}
		} // Never recursively remove a worktree directory.
		if old.Missing {
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if old.Mode&os.ModeSymlink != 0 {
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				return e
			}
			if e = os.Symlink(old.Link, path); e != nil {
				return e
			}
			continue
		}
		tmp, e := os.CreateTemp(filepath.Dir(path), ".git-ez-restore-")
		if e != nil {
			return e
		}
		temporary := tmp.Name()
		if old.Artifact != "" {
			source, openErr := openRegular(old.Artifact)
			e = openErr
			if e == nil {
				_, e = io.Copy(tmp, source)
				_ = source.Close()
			}
		} else {
			var r process.Result
			r, e = process.Run(ctx, process.Request{Args: []string{"git", "cat-file", "blob", old.OID}, Dir: s.g.Dir, Env: git.Environment(s.g.Env, true), Stdout: tmp})
			if e == nil && r.ExitCode != 0 {
				e = fmt.Errorf("Cannot restore saved Git blob")
			}
		}
		if closeErr := tmp.Close(); e == nil {
			e = closeErr
		}
		if e == nil {
			e = os.Chmod(temporary, old.Mode)
		}
		if e == nil {
			e = os.Chtimes(temporary, old.Accessed, old.Modified)
		}
		if e == nil {
			e = os.Rename(temporary, path)
		}
		_ = os.Remove(temporary)
		if e != nil {
			return e
		}
	}
	for _, name := range controlNames {
		if e := reconcile(filepath.Join(s.Directory, "control", name), filepath.Join(s.metadata, name)); e != nil {
			return e
		}
	}
	if s.shared != "" {
		if e := atomicFile(filepath.Join(s.Directory, "shared-index"), s.shared); e != nil {
			return e
		}
	}
	if e := atomicFile(filepath.Join(s.Directory, "index"), s.index); e != nil {
		return e
	}
	actual, e := s.g.Checked(ctx, "ls-files", "--stage", "-z")
	if e != nil {
		return e
	}
	if !bytes.Equal(actual.Stdout, s.entries) {
		return fmt.Errorf("Restored index entries differ from conflict snapshot")
	}
	return nil
}
func (s *State) Close() error { return os.RemoveAll(s.Directory) }
func openRegular(path string) (*os.File, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("Snapshot source is not regular")
	}
	return f, nil
}
func copyFileMode(source, target string) error {
	info, e := os.Lstat(source)
	if e != nil {
		return e
	}
	return copyFile(source, target, info.Mode())
}
func copyFile(source, target string, mode os.FileMode) error {
	f, e := openRegular(source)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return e
	}
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(out, f)
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Chmod(target, mode)
	}
	if e == nil {
		e = os.Chtimes(target, accessTime(info), info.ModTime())
	}
	return e
}
func atomicFile(source, target string) error {
	f, e := os.CreateTemp(filepath.Dir(target), "index.ez-")
	if e != nil {
		return e
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	defer os.Remove(name)
	if e = copyFileMode(source, name); e != nil {
		return e
	}
	return os.Rename(name, target)
}
func copyNode(source, target string) error {
	info, e := os.Lstat(source)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, e := os.Readlink(source)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		return os.Symlink(link, target)
	}
	if info.IsDir() {
		if e = os.MkdirAll(target, 0700); e != nil {
			return e
		}
		entries, e := os.ReadDir(source)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = copyNode(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); e != nil {
				return e
			}
		}
		return os.Chmod(target, info.Mode().Perm())
	}
	return copyFile(source, target, info.Mode())
}
func reconcile(source, target string) error {
	info, e := os.Lstat(source)
	if os.IsNotExist(e) {
		return os.RemoveAll(target)
	}
	if e != nil {
		return e
	}
	if !info.IsDir() {
		if e = os.RemoveAll(target); e != nil {
			return e
		}
		return copyNode(source, target)
	}
	existing, e := os.Lstat(target)
	if e == nil && (!existing.IsDir() || existing.Mode()&os.ModeSymlink != 0) {
		if e = os.Remove(target); e != nil {
			return e
		}
	}
	if e = os.MkdirAll(target, 0700); e != nil {
		return e
	}
	entries, e := os.ReadDir(target)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		_, e := os.Lstat(filepath.Join(source, entry.Name()))
		if os.IsNotExist(e) {
			if e = os.RemoveAll(filepath.Join(target, entry.Name())); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
	}
	entries, e = os.ReadDir(source)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = reconcile(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); e != nil {
			return e
		}
	}
	return os.Chmod(target, info.Mode().Perm())
}
func nodeDigest(path string) (string, error) {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return "absent", nil
	}
	if e != nil {
		return "", e
	}
	h := sha256.New()
	if info.Mode()&os.ModeSymlink != 0 {
		link, e := os.Readlink(path)
		return "link:" + link, e
	}
	if info.IsDir() {
		entries, e := os.ReadDir(path)
		if e != nil {
			return "", e
		}
		for _, entry := range entries {
			value, e := nodeDigest(filepath.Join(path, entry.Name()))
			if e != nil {
				return "", e
			}
			fmt.Fprintf(h, "%q:%q;", entry.Name(), value)
		}
	} else {
		f, e := openRegular(path)
		if e != nil {
			return "", e
		}
		_, e = io.Copy(h, f)
		_ = f.Close()
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
