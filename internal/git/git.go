// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package git delegates repository semantics to the native Git executable.
package git

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/process"
)

type Client struct {
	Dir string
	Env []string
}

func New(directory string) (Client, error) {
	if directory == "" {
		directory = "."
	}
	// Resolve before Abs: lexical cleaning would erase symlink/.. semantics.
	p, e := filepath.EvalSymlinks(directory)
	if e != nil {
		return Client{}, e
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return Client{}, e
	}
	return Client{Dir: p}, nil
}
func (c Client) environment(assisted bool) []string {
	base := c.Env
	if base == nil {
		base = os.Environ()
	}
	return Environment(base, assisted)
}

// Environment only removes checkout-redirection settings. Native Git config,
// identities, hooks, signing and transport settings remain the caller's.
func Environment(base []string, assisted bool) []string {
	out := make([]string, 0, len(base)+3)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX":
			continue
		}
		if assisted && (key == "GIT_TERMINAL_PROMPT" || key == "GIT_EDITOR" || key == "GIT_SEQUENCE_EDITOR") {
			continue
		}
		out = append(out, entry)
	}
	if assisted {
		out = append(out, "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	}
	return out
}
func (c Client) Run(ctx context.Context, args ...string) (process.Result, error) {
	return c.RunWithFiles(ctx, nil, "", args...)
}

// RunWithFiles scopes the capture-only sequence editor and inherited descriptors
// to this invocation. os/exec exposes ExtraFiles at descriptors 3, 4, ... .
func (c Client) RunWithFiles(ctx context.Context, files []*os.File, sequenceEditor string, args ...string) (process.Result, error) {
	return c.RunWithFilesStreams(ctx, files, sequenceEditor, nil, nil, args...)
}

// RunWithFilesStreams mirrors native output while retaining the engine capture.
func (c Client) RunWithFilesStreams(ctx context.Context, files []*os.File, sequenceEditor string, out, diagnostics io.Writer, args ...string) (process.Result, error) {
	env := c.environment(true)
	if sequenceEditor != "" {
		for i, s := range env {
			if strings.HasPrefix(s, "GIT_SEQUENCE_EDITOR=") {
				env[i] = "GIT_SEQUENCE_EDITOR=" + sequenceEditor
			}
		}
	}
	argv := append([]string{"git", "--literal-pathspecs", "-c", "merge.conflictStyle=diff3"}, args...)
	// Native Git retains its process-group semantics, including hooks. Owned
	// agent/check subprocess groups are a separate explicit process.Run mode.
	request := process.Request{Args: argv, Dir: c.Dir, Env: env, ExtraFiles: files, Foreground: true}
	if out == nil && diagnostics == nil {
		return process.Run(ctx, request)
	}
	var stdout, stderr streamCapture
	ow, ew := io.Writer(&stdout), io.Writer(&stderr)
	if out != nil {
		ow = io.MultiWriter(out, &stdout)
	}
	if diagnostics != nil {
		ew = io.MultiWriter(diagnostics, &stderr)
	}
	request.Stdout, request.Stderr = ow, ew
	result, err := process.Run(ctx, request)
	result.Stdout, result.Stderr = stdout.snapshot(), stderr.snapshot()
	return result, err
}

// A cleanup-uncertain process may still own a pipe writer when Run returns.
// Snapshot captures without racing those writers or assuming they have stopped.
type streamCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (c *streamCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.Write(p)
}
func (c *streamCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buffer.Bytes()...)
}

func (c Client) Checked(ctx context.Context, args ...string) (process.Result, error) {
	r, e := c.Run(ctx, args...)
	if e != nil {
		return r, e
	}
	if r.ExitCode != 0 {
		op := "command"
		if len(args) > 0 {
			op = args[0]
		}
		return r, failure.New(failure.Execution, "Git "+op+" failed: "+strings.TrimSpace(strings.ToValidUTF8(string(r.Stderr), "�")))
	}
	return r, nil
}
func (c Client) Text(ctx context.Context, args ...string) (string, error) {
	r, e := c.Checked(ctx, args...)
	if e != nil {
		return "", e
	}
	if !utf8.Valid(r.Stdout) {
		return "", failure.New(failure.Execution, "Git returned non-UTF-8 text")
	}
	return strings.TrimSpace(string(r.Stdout)), nil
}
func (c Client) Foreground(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) (process.Result, error) {
	return process.Run(ctx, process.Request{Args: append([]string{"git"}, args...), Dir: c.Dir, Env: c.environment(false), Stdin: stdin, Stdout: stdout, Stderr: stderr, Foreground: true})
}
func (c Client) Root(ctx context.Context) (Client, error) {
	root, e := c.Text(ctx, "rev-parse", "--show-toplevel")
	if e != nil {
		return Client{}, e
	}
	other, e := New(root)
	if e != nil {
		return Client{}, e
	}
	if c.Env != nil {
		other.Env = append([]string{}, c.Env...)
	}
	return other, nil
}
func (c Client) GitDir(ctx context.Context) (string, error) {
	return c.Text(ctx, "rev-parse", "--absolute-git-dir")
}
func (c Client) CommonDir(ctx context.Context) (string, error) {
	return c.Text(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
}
func (c Client) Path(ctx context.Context, name string) (string, error) {
	// --path-format=absolute canonicalizes the leaf on recent Git versions,
	// hiding a symlink from a later O_NOFOLLOW open. Resolve the lexical mapping
	// ourselves so metadata readers can actually enforce their non-follow policy.
	p, e := c.Text(ctx, "rev-parse", "--git-path", name)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.Dir, p)
	}
	return p, nil
}
func (c Client) CommitOID(ctx context.Context, revision string) (string, error) {
	return c.Text(ctx, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
}
func (c Client) BranchOID(ctx context.Context, branch string) (string, error) {
	r, e := c.Run(ctx, "check-ref-format", "--branch", branch)
	if e != nil {
		return "", e
	}
	if r.ExitCode != 0 {
		return "", failure.New(failure.Precondition, "Invalid branch name: "+branch)
	}
	return c.Text(ctx, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
}

type Stage struct {
	Mode string `json:"mode"`
	OID  string `json:"oid"`
}
type Conflicts map[string]map[int]Stage

func ParseUnmerged(raw []byte) (Conflicts, error) {
	out := Conflicts{}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return nil, failure.New(failure.Execution, "Malformed Git index entry")
		}
		if !utf8.Valid(path) {
			return nil, failure.New(failure.Precondition, "Non-UTF-8 filenames require manual resolution")
		}
		parts := strings.Fields(string(header))
		if len(parts) != 3 {
			return nil, failure.New(failure.Execution, "Malformed Git index entry")
		}
		stage, e := strconv.Atoi(parts[2])
		if e != nil || stage < 1 || stage > 3 {
			return nil, failure.New(failure.Execution, "Invalid Git conflict stage")
		}
		name := string(path)
		if out[name] == nil {
			out[name] = map[int]Stage{}
		}
		out[name][stage] = Stage{parts[0], parts[1]}
	}
	return out, nil
}
func (c Client) Conflicts(ctx context.Context) (Conflicts, error) {
	r, e := c.Checked(ctx, "ls-files", "--unmerged", "-z")
	if e != nil {
		return nil, e
	}
	return ParseUnmerged(r.Stdout)
}
func Paths(raw []byte) ([]string, error) {
	if !utf8.Valid(raw) {
		return nil, failure.New(failure.Precondition, "Non-UTF-8 changed filenames require manual resolution")
	}
	if len(raw) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), nil
}
func (c Client) SafePath(name string) (string, error) {
	if strings.HasPrefix(name, "/") {
		return "", failure.New(failure.Precondition, "Unsafe conflict path: "+name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return "", failure.New(failure.Precondition, "Unsafe conflict path: "+name)
		}
	}
	p := filepath.Join(c.Dir, filepath.FromSlash(name))
	relative, e := filepath.Rel(c.Dir, p)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", failure.New(failure.Precondition, "Conflict path escapes worktree")
	}
	for q := p; q != c.Dir; q = filepath.Dir(q) {
		info, e := os.Lstat(q)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", failure.New(failure.Precondition, "Symlinks require manual resolution: "+name)
		}
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	return p, nil
}
func (c Client) Blob(ctx context.Context, s Stage, limit int64) ([]byte, error) {
	size, e := c.Text(ctx, "cat-file", "-s", s.OID)
	if e != nil {
		return nil, e
	}
	n, e := strconv.ParseInt(size, 10, 64)
	if e != nil {
		return nil, e
	}
	if n > limit {
		return nil, failure.New(failure.Precondition, "Conflict file exceeds configured size limit")
	}
	r, e := c.Checked(ctx, "cat-file", "blob", s.OID)
	return r.Stdout, e
}
func (c Client) HasMarker(ctx context.Context, name string) (bool, error) {
	p, e := c.Path(ctx, name)
	if e != nil {
		return false, e
	}
	_, e = os.Lstat(p)
	if os.IsNotExist(e) {
		return false, nil
	}
	return e == nil, e
}
func (c Client) Active(ctx context.Context) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer", "BISECT_START"} {
		yes, e := c.HasMarker(ctx, name)
		if e != nil {
			return false, e
		}
		if yes {
			return true, nil
		}
	}
	return false, nil
}
func (c Client) IntegrationActive(ctx context.Context, operation string) (bool, error) {
	var names []string
	switch operation {
	case "rebase":
		names = []string{"rebase-merge", "rebase-apply"}
	case "merge":
		names = []string{"MERGE_HEAD"}
	case "cherry-pick":
		names = []string{"CHERRY_PICK_HEAD", "sequencer"}
	default:
		return false, failure.New(failure.Usage, "Unsupported integration operation")
	}
	for _, name := range names {
		yes, e := c.HasMarker(ctx, name)
		if e != nil {
			return false, e
		}
		if yes {
			return true, nil
		}
	}
	return false, nil
}

// ReadMetadata refuses symlinks and bounded/nonregular metadata. Interpreting
// rebase counters/identity to admit later assistance belongs to the engine.
func (c Client) ReadMetadata(ctx context.Context, name string, limit int64) ([]byte, error) {
	p, e := c.Path(ctx, name)
	if e != nil {
		return nil, e
	}
	f, e := openRegular(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("Git metadata exceeds capture limit")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("Git metadata exceeds capture limit")
	}
	return b, e
}
