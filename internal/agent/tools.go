// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/ai/tool"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/process"
)

func (s *session) controlState(ctx context.Context) (string, error) {
	h := sha256.New()
	g := s.g
	for _, args := range [][]string{{"rev-parse", "HEAD"}, {"symbolic-ref", "-q", "HEAD"}, {"ls-files", "--stage", "-z"}} {
		r, e := g.Run(ctx, args...)
		if e != nil {
			return "", e
		}
		if r.ExitCode != 0 && args[0] != "symbolic-ref" {
			return "", fail("Cannot inspect agent Git control state")
		}
		_, _ = h.Write(r.Stdout)
		_, _ = h.Write([]byte{0})
	}
	for _, p := range []struct{ ref, oid string }{{"refs/heads/" + s.stop.Branch, s.stop.BeforeOID}, {s.stop.Target.Reference, s.stop.Target.OID}} {
		if p.ref != "" {
			oid, e := g.CommitOID(ctx, p.ref)
			if e != nil {
				return "", e
			}
			_, _ = h.Write([]byte(p.ref + "=" + oid))
			if p.oid != "" && oid != p.oid {
				return "", fail("Authoritative engine reference no longer matches")
			}
		}
	}
	if s.stop.Onto != nil && s.stop.Onto.Reference != "" {
		oid, e := g.CommitOID(ctx, s.stop.Onto.Reference)
		if e != nil {
			return "", e
		}
		if oid != s.stop.Onto.OID {
			return "", fail("Onto reference changed")
		}
	}
	for _, name := range []string{"MERGE_HEAD", "MERGE_MSG", "CHERRY_PICK_HEAD", "REBASE_HEAD", "rebase-merge", "rebase-apply", "sequencer", "config", "config.worktree", "ORIG_HEAD", "REVERT_HEAD", "BISECT_START", "index.lock"} {
		p, e := g.Path(ctx, name)
		if e != nil {
			return "", e
		}
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return "", e
		}
		e = filepath.WalkDir(p, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			raw, e := journal.Read(path, 10_000_000)
			if e != nil {
				return e
			}
			_, _ = h.Write([]byte(path))
			_, _ = h.Write(raw)
			return nil
		})
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *session) changed(ctx context.Context) ([]string, error) {
	r, e := s.g.Checked(ctx, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", "HEAD", "--")
	if e != nil {
		return nil, e
	}
	u, e := s.g.Checked(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if e != nil {
		return nil, e
	}
	paths, e := git.Paths(append(r.Stdout, u.Stdout...))
	if e != nil {
		return nil, e
	}
	for _, c := range s.stop.Conflicts {
		paths = append(paths, c.Path)
	}
	sort.Strings(paths)
	out := []string{}
	for _, p := range paths {
		if len(out) == 0 || out[len(out)-1] != p {
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *session) worktreeState(ctx context.Context) (string, error) {
	paths, e := s.changed(ctx)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	for _, name := range paths {
		p, e := s.g.SafePath(name)
		if e != nil {
			continue
		}
		raw, e := journal.Read(p, 10_000_000)
		if os.IsNotExist(e) {
			_, _ = h.Write([]byte(name + "=deleted"))
			continue
		}
		if e != nil {
			continue
		}
		info, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		_, _ = fmt.Fprintf(h, "%s:%o:", name, info.Mode().Perm())
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var conflictMarkers = regexp.MustCompile(`(?m)^(?:<{7,}|={7,}|>{7,}|\|{7,})`)

func (s *session) verifyProposal(ctx context.Context) error {
	if e := s.guard(ctx); e != nil {
		return e
	}
	paths, e := s.changed(ctx)
	if e != nil {
		return e
	}
	isConflict := map[string]bool{}
	for _, c := range s.stop.Conflicts {
		isConflict[c.Path] = true
	}
	for path := range s.initialFiles {
		paths = append(paths, path)
	}
	for _, name := range paths {
		p, e := s.g.SafePath(name)
		if e != nil {
			return e
		}
		fingerprint, fpErr := fileFingerprint(p)
		if old, exists := s.initialFiles[name]; exists && !isConflict[name] && fpErr == nil && old == fingerprint {
			continue
		}
		b, e := journal.Read(p, int64(s.config.MaxFileBytes))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return fail("Proposal requires bounded regular text: " + name)
		}
		if !utf8.Valid(b) || bytes.ContainsRune(b, 0) || conflictMarkers.Match(b) {
			return fail("Conflict markers, binary or non-UTF-8 proposal remain: " + name)
		}
	}
	return nil
}
func fileFingerprint(path string) (string, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(e) {
		return "deleted", nil
	}
	if e != nil {
		return "", e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return "", fail("Not regular worktree data")
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%o:", info.Mode().Perm())
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *session) fileState(ctx context.Context) (map[string]string, error) {
	paths, e := s.changed(ctx)
	if e != nil {
		return nil, e
	}
	out := map[string]string{}
	for _, name := range paths {
		p, e := s.g.SafePath(name)
		if e != nil {
			continue
		}
		fingerprint, e := fileFingerprint(p)
		if e == nil {
			out[name] = fingerprint
		}
	}
	return out, nil
}

func (s *session) validateToolPath(input any) error {
	b, e := json.Marshal(input)
	if e != nil {
		return e
	}
	var fields map[string]any
	if e = json.Unmarshal(b, &fields); e != nil {
		return e
	}
	for _, key := range []string{"filePath", "dirPath"} {
		if path, ok := fields[key].(string); ok {
			if _, e = s.g.SafePath(strings.TrimLeft(path, "/")); e != nil {
				return fail("Filesystem access to metadata, symlinks or paths outside the worktree is forbidden")
			}
		}
	}
	return nil
}
func (s *session) retain(name string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	written, e := s.evidence.RawEvidence(name, b)
	if e != nil {
		s.fatal = e
		return e
	}
	s.bytes += written
	return nil
}

func (s *session) offloadResult(result *ai.MultipartToolResponse) error {
	b, e := json.Marshal(result)
	if e != nil {
		return e
	}
	if len(b) <= s.config.ToolOutputBytes {
		return nil
	}
	s.artifactID++
	name := fmt.Sprintf("tool-%06d.json", s.artifactID)
	s.mu.Lock()
	written, saveErr := s.evidence.JSONEvidence(name, result)
	if saveErr == nil {
		s.bytes += written
	}
	s.mu.Unlock()
	if saveErr != nil {
		return s.abort(saveErr)
	}
	result.Content = nil
	preview := s.evidence.Redact(b[:min(len(b), s.config.ToolOutputBytes)])
	for {
		result.Output = map[string]any{"artifact": name, "bytes": len(b), "truncated": true, "preview": string(preview)}
		encoded, _ := json.Marshal(result)
		if len(encoded) <= s.config.ToolOutputBytes {
			break
		}
		if len(preview) == 0 {
			return s.abort(fail("Tool response envelope exceeds output budget"))
		}
		preview = preview[:len(preview)/2]
	}
	return nil
}

type shellInput struct {
	Command string  `json:"command"`
	Timeout float64 `json:"timeout,omitempty"`
}
type shellResult struct {
	ExitCode                                   int `json:"exit_code"`
	Stdout, Stderr                             string
	StdoutArtifact, StderrArtifact             string
	StdoutTruncated, StderrTruncated, TimedOut bool
}

var forbiddenGit = regexp.MustCompile(`(?i)(?:^|[\s/"';&|])git\s+(?:-c\b|-C\b|--(?:git-dir|work-tree)\b|add\b|commit\b|checkout\b|switch\b|reset\b|restore\b|merge\b|rebase\b|cherry-pick\b|revert\b|update-ref\b|symbolic-ref\s+[^-]|fetch\b|pull\b|push\b|stash\b|worktree\b|clean\b|am\b|apply\b)`)

func (s *session) shell(ctx context.Context, in shellInput) (shellResult, error) {
	var out shellResult
	if strings.TrimSpace(in.Command) == "" || forbiddenGit.MatchString(in.Command) {
		return out, tool.Fail(ctx, fail("Git lifecycle mutation and cross-worktree commands are forbidden"))
	}
	if in.Timeout < 0 || in.Timeout > s.config.Timeout {
		return out, tool.Fail(ctx, fail("Command timeout exceeds configured limit"))
	}
	timeout := s.config.TimeoutDuration()
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout * float64(time.Second))
	}
	out.StdoutArtifact = fmt.Sprintf("command-%06d.stdout", s.executed)
	out.StderrArtifact = fmt.Sprintf("command-%06d.stderr", s.executed)
	stdout, err := s.evidence.Capture(out.StdoutArtifact)
	if err != nil {
		return out, s.abort(err)
	}
	stderr, err := s.evidence.Capture(out.StderrArtifact)
	if err != nil {
		stdout.Abort()
		return out, s.abort(err)
	}
	defer stdout.Close()
	defer stderr.Close()
	started := time.Now()
	commandID := strings.TrimSuffix(out.StdoutArtifact, ".stdout")
	if e := s.record("command_started", map[string]any{"command_id": commandID, "command": in.Command, "cwd": s.g.Dir, "timeout": timeout.Seconds()}); e != nil {
		return out, e
	}
	environment, envErr := s.shellEnvironment()
	if envErr != nil {
		return out, s.abort(envErr)
	}
	r, e := process.Run(ctx, process.Request{Args: []string{"/bin/sh", "-c", in.Command}, Dir: s.g.Dir, Env: environment, Stdout: stdout, Stderr: stderr, Timeout: timeout})
	if failure.IsKind(e, failure.Cleanup) {
		s.abort(e)
	}
	out.ExitCode = r.ExitCode
	out.TimedOut = r.TimedOut
	if out.TimedOut {
		out.ExitCode = 124
	}
	if save := errors.Join(stdout.Close(), stderr.Close()); save != nil {
		return out, s.abort(errors.Join(e, save))
	}
	s.mu.Lock()
	s.bytes += int(stdout.Bytes + stderr.Bytes)
	s.mu.Unlock()
	stdoutPreview, readErr := s.evidence.Window(out.StdoutArtifact, 0, int64(s.config.ToolOutputBytes))
	if readErr != nil {
		return out, s.abort(readErr)
	}
	stderrPreview, readErr := s.evidence.Window(out.StderrArtifact, 0, int64(s.config.ToolOutputBytes))
	if readErr != nil {
		return out, s.abort(readErr)
	}
	out.Stdout = string(stdoutPreview)
	out.Stderr = string(stderrPreview)
	out.StdoutTruncated = r.StdoutTruncated || stdout.Bytes > int64(len(stdoutPreview))
	out.StderrTruncated = r.StderrTruncated || stderr.Bytes > int64(len(stderrPreview))
	if save := s.record("command_finished", map[string]any{"command_id": commandID, "duration_seconds": time.Since(started).Seconds(), "command": in.Command, "result": out, "stdout_bytes": stdout.Bytes, "stderr_bytes": stderr.Bytes}); save != nil {
		return out, save
	}
	if failure.IsKind(e, failure.Cleanup) {
		return out, s.abort(e)
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if e != nil && !r.TimedOut {
		return out, s.abort(e)
	}
	return out, nil
}
func (s *session) shellEnvironment() ([]string, error) {
	base := s.g.Env
	if base == nil {
		base = os.Environ()
	}
	original := git.Environment(base, true)
	out := make([]string, 0, len(original))
	for _, entry := range original {
		k, v, _ := strings.Cut(entry, "=")
		if k == "PYTHONDONTWRITEBYTECODE" || k == s.config.APIKeyEnv || strings.HasPrefix(k, "GENKIT_") || strings.HasPrefix(k, "OTEL_") {
			continue
		}
		if strings.HasPrefix(k, "GIT_CONFIG_VALUE_") {
			suffix := strings.TrimPrefix(k, "GIT_CONFIG_VALUE_")
			key, _ := environmentLookup(base)("GIT_CONFIG_KEY_" + suffix)
			if strings.EqualFold(key, "ez.apiKey") {
				v = "<redacted>"
			}
		}
		// Keep all other caller scopes while preventing explicit keys in Git's shell
		// parameter transport from entering a child process.
		if k == "GIT_CONFIG_PARAMETERS" {
			filtered, e := filterGitParameters(v)
			if e != nil {
				return nil, e
			}
			v = filtered
		}
		out = append(out, k+"="+v)
	}
	return append(out, "PYTHONDONTWRITEBYTECODE=1"), nil
}

type artifactInput struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
}

func (s *session) readArtifact(in artifactInput) (string, error) {
	if filepath.Base(in.Path) != in.Path || in.Offset < 0 || !strings.HasPrefix(in.Path, "tool-") && !strings.HasPrefix(in.Path, "command-") {
		return "", tool.Fail(context.Background(), fail("Invalid artifact name or offset"))
	}
	b, e := s.evidence.Window(in.Path, in.Offset, int64(max(1, s.config.ToolOutputBytes/8)))
	if e != nil {
		return "", tool.Fail(context.Background(), fail("Artifact unavailable or offset outside retained bytes"))
	}
	return string(b), nil
}
