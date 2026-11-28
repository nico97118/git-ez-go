// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/nico97118/git-ez-go/internal/conflictstate"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/process"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/sequence"
	"github.com/nico97118/git-ez-go/internal/terminal"
)

var markers = regexp.MustCompile(`(?m)^(?:<{7,}|={7,}|>{7,}|\|{7,}).*$`)

type fingerprint struct {
	Mode os.FileMode `json:"mode"`
	Hash string      `json:"sha256"`
}
type fileEvidence struct {
	Path        string       `json:"path"`
	Content     *string      `json:"content"`
	Fingerprint *fingerprint `json:"fingerprint"`
}

func (s *session) changed() ([]string, error) {
	r, e := s.engine.Git.Checked(s.ctx, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", "HEAD", "--")
	if e != nil {
		return nil, e
	}
	u, e := s.engine.Git.Checked(s.ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if e != nil {
		return nil, e
	}
	paths, e := git.Paths(append(r.Stdout, u.Stdout...))
	if e != nil {
		return nil, e
	}
	return unique(paths), nil
}
func unique(paths []string) []string {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	a := make([]string, 0, len(m))
	for p := range m {
		a = append(a, p)
	}
	sort.Strings(a)
	return a
}
func (s *session) fingerprint(name string) (*fingerprint, error) {
	p, e := s.engine.Git.SafePath(name)
	if e != nil {
		return nil, e
	}
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, stopped("Changed file is not regular text: " + name)
	}
	// Fingerprints are streamed so unchanged unsupported large/binary resources do
	// not become proposals or require unbounded in-memory copies.
	fd, e := syscall.Open(p, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !actual.Mode().IsRegular() {
		return nil, stopped("Changed file is not regular text")
	}
	digest := sha256.New()
	if _, e = io.Copy(digest, f); e != nil {
		return nil, e
	}
	return &fingerprint{Mode: actual.Mode().Perm(), Hash: hex.EncodeToString(digest.Sum(nil))}, nil
}
func same(a, b *fingerprint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
func (s *session) baseline() (map[string]*fingerprint, error) {
	paths, e := s.changed()
	if e != nil {
		return nil, e
	}
	out := map[string]*fingerprint{}
	for _, p := range paths {
		fp, e := s.fingerprint(p)
		if e == nil {
			out[p] = fp
		}
	}
	return out, nil
}
func (s *session) conflict(name string, versions map[int]git.Stage) (resolver.Conflict, error) {
	c := resolver.Conflict{Path: name}
	mode := ""
	for n, v := range versions {
		if (v.Mode != "100644" && v.Mode != "100755") || (mode != "" && mode != v.Mode) {
			return c, stopped("Non-regular file or mode conflict requires manual resolution: " + name)
		}
		mode = v.Mode
		raw, e := s.engine.Git.Blob(s.ctx, v, 10_000_000)
		if e != nil {
			return c, e
		}
		if !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
			return c, stopped("Binary or non-UTF-8 conflict requires manual resolution: " + name)
		}
		text := string(raw)
		switch n {
		case 1:
			c.Base = &text
		case 2:
			c.Ours = &text
		case 3:
			c.Theirs = &text
		}
	}
	p, e := s.engine.Git.SafePath(name)
	if e != nil {
		return c, e
	}
	raw, e := journal.Read(p, 10_000_000)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return c, stopped("Binary or non-UTF-8 working conflict requires manual resolution")
	}
	text := string(raw)
	c.Marked = &text
	return c, nil
}
func (s *session) guard(head, symbolic string, index []byte, identity *position) error {
	g := s.engine.Git
	now, e := g.CommitOID(s.ctx, "HEAD")
	if e != nil {
		return e
	}
	ref, e := g.Run(s.ctx, "symbolic-ref", "-q", "HEAD")
	if e != nil {
		return e
	}
	branch, e := g.BranchOID(s.ctx, s.branch)
	if e != nil {
		return e
	}
	if now != head || strings.TrimSpace(string(ref.Stdout)) != symbolic || branch != s.before {
		return stopped("Resolver changed Git control state or input refs")
	}
	if e = s.verifyPins(); e != nil {
		return e
	}
	active, e := g.IntegrationActive(s.ctx, s.request.Operation)
	if e != nil {
		return e
	}
	if !active {
		return stopped("Resolver changed Git operation metadata")
	}
	if s.request.Operation == "rebase" {
		now := s.position()
		if identity == nil || now == nil || *now != *identity {
			return stopped("Resolver changed rebase identity or replay position")
		}
	} else {
		marker := "MERGE_HEAD"
		if s.request.Operation == "cherry-pick" {
			marker = "CHERRY_PICK_HEAD"
			seq, e := g.HasMarker(s.ctx, "sequencer")
			if e != nil {
				return e
			}
			if seq {
				return stopped("Cherry-pick sequencer unexpectedly active")
			}
		}
		oid, e := g.CommitOID(s.ctx, marker)
		if e != nil || oid != s.target.OID {
			return stopped("Resolver changed Git operation target")
		}
	}
	actual, e := g.Checked(s.ctx, "ls-files", "--stage", "-z")
	if e != nil {
		return e
	}
	if !bytes.Equal(index, actual.Stdout) {
		return stopped("Resolver changed the index; staging belongs to the engine")
	}
	if s.capture != nil {
		if e = sequence.Verify(*s.capture); e != nil {
			return e
		}
	}
	return nil
}
func cleanupUncertain(err error) bool { return failure.IsKind(err, failure.Cleanup) }
func (s *session) resolve(stages git.Conflicts, round int) (err error) {
	s.engine.Report.SetSecret(s.engine.Config.ResolvedAPIKey(s.lookupEnv))
	started := time.Now()
	s.result.ConflictSets++
	s.result.ConflictFiles += len(stages)
	s.engine.Report.Info("git-ez: Resolving conflict set %d (%s)", s.result.ConflictSets, terminal.Count(len(stages), "file"))
	s.engine.Report.Info("  Model: %s (%s)", s.engine.Config.Model, s.engine.Config.Provider)
	defer func() {
		elapsed := time.Since(started)
		s.result.ResolutionTime += elapsed
		if err == nil {
			s.engine.Report.Info("git-ez: Conflict set resolved (%.1fs)", elapsed.Seconds())
		}
	}()

	if !s.request.Interactive {
		return s.resolveOnce(stages, round, nil)
	}
	state, e := conflictstate.New(s.ctx, s.engine.Git)
	if e != nil {
		return e
	}
	conflicts := len(s.journal.View["conflicts"].([]any))
	reviews := len(s.journal.View["reviews"].([]any))
	defer func() {
		if err == nil {
			err = state.Close()
			return
		}
		if cleanupUncertain(err) {
			err = failure.Wrap(failure.Cleanup, "Child cleanup failed; conflict was not restored. Backup retained at "+state.Directory, err)
			return
		}
		// NotifyContext remains registered: repeated signals cannot cancel cleanup.
		if restoreErr := state.Restore(context.WithoutCancel(s.ctx)); restoreErr != nil {
			err = errors.Join(err, stopped("Conflict restoration failed; backup retained at "+state.Directory+": "+restoreErr.Error()))
			return
		}
		for _, item := range s.journal.View["conflicts"].([]any)[conflicts:] {
			record := item.(map[string]any)
			record["restoration"] = "original_conflict_restored"
			record["application"] = "unmerged"
		}
		for _, item := range s.journal.View["reviews"].([]any)[reviews:] {
			record := item.(map[string]any)
			record["restoration"] = "original_conflict_restored"
			if record["application"] == "staged" {
				record["application"] = "restored_unmerged"
			}
		}
		err = errors.Join(err, s.journal.Save(), state.Close())
	}()
	return s.resolveOnce(stages, round, state)
}
func (s *session) resolveOnce(stages git.Conflicts, round int, state *conflictstate.State) (err error) {
	g := s.engine.Git
	head, e := g.CommitOID(s.ctx, "HEAD")
	if e != nil {
		return e
	}
	ref, e := g.Run(s.ctx, "symbolic-ref", "-q", "HEAD")
	if e != nil {
		return e
	}
	index, e := g.Checked(s.ctx, "ls-files", "--stage", "-z")
	if e != nil {
		return e
	}
	identity := s.position()
	if s.request.Operation != "rebase" && (head != s.before || strings.TrimSpace(string(ref.Stdout)) != "refs/heads/"+s.branch) {
		return stopped("Native Git changed branch/head before the conflict stop")
	}
	if s.request.Operation == "rebase" && strings.TrimSpace(string(ref.Stdout)) != "" {
		return stopped("Rebase conflict must retain native detached HEAD")
	}
	if e = s.guard(head, strings.TrimSpace(string(ref.Stdout)), index.Stdout, identity); e != nil {
		return e
	}
	baseline, e := s.baseline()
	if e != nil {
		return e
	}
	names := make([]string, 0, len(stages))
	for name := range stages {
		names = append(names, name)
	}
	sort.Strings(names)
	conflicts := make([]resolver.Conflict, 0, len(names))
	records := map[string]map[string]any{}
	for _, name := range names {
		c, e := s.conflict(name, stages[name])
		if e != nil {
			return e
		}
		conflicts = append(conflicts, c)
	}
	if s.engine.Resolver == nil {
		return stopped("No resolver is configured in this native-engine build; use native Git to resolve and continue")
	}
	if e = s.journal.Promote(); e != nil {
		return e
	}
	s.result.Directory = s.journal.Directory
	stopID := journal.ID()
	stop := resolver.Stop{Operation: s.request.Operation, Worktree: g.Dir, Branch: s.branch, BeforeOID: s.before, HeadOID: head, Target: s.target, Conflicts: conflicts, Validation: append([]string{}, s.request.Validation...), ArtifactDirectory: s.journal.Directory}
	environment := g.Env
	if environment == nil {
		environment = os.Environ()
	}
	environment = append([]string{}, environment...)
	stop.Environment = func() []string { return append([]string{}, environment...) }
	stop.Retain = func(ev resolver.Evidence) error {
		relative, e := filepath.Rel(s.journal.Directory, ev.Directory)
		if e != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return stopped("Resolver evidence escaped engine journal")
		}
		records, _ := s.journal.View["sessions"].([]any)
		for _, item := range records {
			record := item.(map[string]any)
			if record["artifacts"] == filepath.ToSlash(relative) {
				record["status"] = ev.Status
				record["model_calls"] = ev.ModelCalls
				record["tool_calls"] = ev.ToolCalls
				return s.journal.Save()
			}
		}
		s.journal.View["sessions"] = appendRecord(s.journal.View["sessions"], map[string]any{"round": round, "runtime": ev.Runtime, "artifacts": filepath.ToSlash(relative), "status": ev.Status, "model_calls": ev.ModelCalls, "tool_calls": ev.ToolCalls})
		return s.journal.Save()
	}
	if s.onto.OID != "" {
		p := s.onto
		stop.Onto = &p
	}
	if s.plan != nil {
		stop.ResultBaseOID = s.plan.BaseOID
		stop.ReplayBoundaryOID = s.plan.SelectionOID
	} else {
		stop.ResultBaseOID = s.target.OID
	}
	for _, c := range conflicts {
		number := len(s.journal.View["conflicts"].([]any)) + 1
		record := map[string]any{"id": journal.ID(), "number": number, "path": c.Path, "round": round, "stop_id": stopID, "status": "pending", "head": head, "artifacts": map[string]any{}}
		records[c.Path] = record
		s.journal.View["conflicts"] = appendRecord(s.journal.View["conflicts"], record)
	}
	if e = s.journal.Save(); e != nil {
		return e
	}
	if state != nil {
		return s.reviewConflicts(stop, stages, round, baseline, records, state, head, strings.TrimSpace(string(ref.Stdout)), index.Stdout, identity)
	}
	if s.engine.Resolver == nil {
		return stopped("No resolver is configured in this native-engine build; use native Git to resolve and continue")
	}
	number := len(s.journal.View["reviews"].([]any)) + 1
	child, e := s.journal.Child(fmt.Sprintf("proposal-%04d", number))
	if e != nil {
		return e
	}
	defer child.Close()
	review := map[string]any{"id": journal.ID(), "round": round, "attempt": number, "stop_id": stopID, "status": "running", "application": "not_staged", "diff_base_oid": head, "artifacts": filepath.Base(child.Directory)}
	ids := []string{}
	for _, c := range conflicts {
		ids = append(ids, records[c.Path]["id"].(string))
	}
	review["conflict_ids"] = ids
	s.journal.View["reviews"] = appendRecord(s.journal.View["reviews"], review)
	if e = s.journal.Save(); e != nil {
		return e
	}
	originalRetain := stop.Retain
	stop.Retain = func(ev resolver.Evidence) error {
		if err := originalRetain(ev); err != nil {
			return err
		}
		sessions := s.journal.View["sessions"].([]any)
		last := sessions[len(sessions)-1].(map[string]any)
		last["attempt"] = number
		last["stop_id"] = stopID
		review["session"] = last["artifacts"]
		return s.journal.Save()
	}
	s.result.ResolverCalls++
	decision, resolveErr := s.resolveAttempt(stop, 1)
	review["resolution"] = decision
	review["status"] = "proposed"
	if e = child.JSON("decision.json", decision); e != nil {
		return errors.Join(e, resolveErr)
	}
	defer func() {
		if review["application"] != "staged" {
			if err != nil {
				review["error"] = string(s.journal.Redact([]byte(err.Error())))
			}
			review["status"] = "failed"
			if s.ctx.Err() != nil {
				review["status"] = "interrupted"
			}
			if s.ctx.Err() == nil && resolveErr == nil && decision.Validate() == nil && decision.Confidence < s.engine.Config.MinConfidence {
				review["status"] = "low_confidence"
			}
			if !cleanupUncertain(resolveErr) {
				if captureErr := s.captureFailedAttempt(child, stages, baseline); captureErr != nil {
					review["capture_error"] = "Failed proposal capture unavailable"
					err = errors.Join(err, captureErr)
				}
			}
			_ = s.journal.Save()
		}
	}()
	if e = s.journal.Save(); e != nil {
		return errors.Join(e, resolveErr)
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if resolveErr != nil {
		return failure.Wrap(failure.Execution, "Resolver failed; worktree preserved", resolveErr)
	}
	if e = s.ctx.Err(); e != nil {
		return e
	}
	s.engine.Report.Info("git-ez: Validating proposal...")
	if e = decision.Validate(); e != nil {
		return e
	}
	if decision.Confidence < s.engine.Config.MinConfidence {
		return stopped("Resolver confidence below threshold; worktree preserved")
	}
	if e = s.guard(head, strings.TrimSpace(string(ref.Stdout)), index.Stdout, identity); e != nil {
		return e
	}
	selected, evidence, e := s.proposalFiles(stages, baseline, nil)
	if e != nil {
		return e
	}
	review["diff_base_oid"] = head
	if _, _, e = s.captureReview(child, decision, selected, evidence); e != nil {
		return e
	}
	review["decision"] = "automatic_accept"
	if e = s.journal.Save(); e != nil {
		return e
	}
	// Detect mutations during evidence capture, including Git filters that will
	// subsequently run during staging. Audit failure never permits staging.
	if e = s.guard(head, strings.TrimSpace(string(ref.Stdout)), index.Stdout, identity); e != nil {
		return e
	}
	for _, f := range evidence {
		fp, e := s.fingerprint(f.Path)
		if e != nil {
			return e
		}
		if !same(fp, f.Fingerprint) {
			return stopped("Proposed contents changed before staging")
		}
	}
	if _, e = g.Checked(s.ctx, append([]string{"add", "-A", "--"}, selected...)...); e != nil {
		return e
	}
	review["decision"] = "automatic_accept"
	review["status"] = "applied"
	review["application"] = "staged"
	review["paths"] = selected
	for _, f := range evidence {
		if record := records[f.Path]; record != nil {
			record["status"] = "applied"
			record["application"] = "staged"
		}
	}
	return s.journal.Save()
}

type validationCapture struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (c *validationCapture) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, c.limit-c.buffer.Len())
	if n > remaining {
		c.truncated = true
	}
	_, e := c.buffer.Write(p[:min(n, remaining)])
	return n, e
}
func runValidation(ctx context.Context, g git.Client, args []string, timeout time.Duration, stdout, stderr io.Writer) (process.Result, error) {
	env := g.Env
	if env == nil {
		env = os.Environ()
	}
	return process.Run(ctx, process.Request{Args: args, Dir: g.Dir, Env: git.Environment(env, true), Timeout: timeout, Stdout: stdout, Stderr: stderr})
}
