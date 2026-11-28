// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package engine owns native Git lifecycle and verification. Resolvers propose
// filesystem edits; they never decide whether Git completed or may continue.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/locking"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/review"
	"github.com/nico97118/git-ez-go/internal/sequence"
	"github.com/nico97118/git-ez-go/internal/terminal"
)

type Request struct {
	Operation, Target     string
	Onto                  *string
	Rebase                options.Rebase
	Commit                options.Commit
	MergeFF               string
	Rerere                options.Toggle
	Validation            []string
	Continue, Interactive bool
}
type Result struct {
	Directory, Status, BeforeOID, ResultOID string
	Integrated                              bool
	ResolverCalls                           int
	ConflictSets, ConflictFiles             int
	ResolutionTime                          time.Duration
	FinalValidationFailed                   bool
	NativeStatus                            *int
}
type Engine struct {
	Report   *terminal.Reporter
	Git      git.Client
	Config   config.Config
	Resolver resolver.Resolver
	In       io.Reader
	Out, Err io.Writer
}
type session struct {
	engine         *Engine
	request        Request
	ctx            context.Context
	journal        *journal.Journal
	result         Result
	branch, before string
	target, onto   resolver.Pin
	plan           *Plan
	capture        *sequence.Capture
	autoResolved   bool
	rerere         bool
	prefix         []string
	reviewInput    *review.Input
}

func New(ctx context.Context, g git.Client, c config.Config, r resolver.Resolver) (*Engine, error) {
	root, e := g.Root(ctx)
	if e != nil {
		return nil, failure.Wrap(failure.Precondition, "Cannot discover repository worktree", e)
	}
	g = root
	return &Engine{Git: g, Config: c, Resolver: r, In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, nil
}
func bad(message string) error     { return failure.New(failure.Precondition, message) }
func stopped(message string) error { return failure.New(failure.Execution, message) }
func (e *Engine) Run(ctx context.Context, q Request) (result Result, err error) {
	defer func() {
		if err != nil && result.Status == "" && !result.Integrated {
			var domain *failure.Error
			if errors.As(err, &domain) && domain.Kind == failure.Execution {
				err = failure.Wrap(failure.Precondition, err.Error(), err)
			}
		}
	}()
	if er := validateRequest(q); er != nil {
		return result, er
	}
	if er := e.Git.RequireSupported(ctx); er != nil {
		return result, er
	}
	lock, er := locking.Acquire(ctx, e.Git)
	if er != nil {
		return result, er
	}
	defer func() {
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
	}()
	s := &session{engine: e, request: q, ctx: ctx}
	if q.Continue {
		return s.continueNative()
	}
	if er = e.Config.Validate(); er != nil {
		return result, er
	}
	if s.branch, er = s.admit(); er != nil {
		return result, er
	}
	if s.before, er = e.Git.BranchOID(ctx, s.branch); er != nil {
		return result, er
	}
	if s.target, er = s.pin(q.Target, false); er != nil {
		return result, er
	}
	if q.Onto != nil {
		if s.onto, er = s.pin(*q.Onto, false); er != nil {
			return result, er
		}
	}
	if q.Operation == "cherry-pick" {
		parents, er := e.Git.Text(ctx, "rev-list", "--parents", "-n", "1", s.target.OID)
		if er != nil {
			return result, er
		}
		if len(strings.Fields(parents)) > 2 {
			return result, bad("Cherry-picking merge commits is unsupported; use native Git")
		}
	} else if q.Operation != "rebase" || q.Rebase.KeepBase != options.Enabled {
		if _, er = e.Git.Checked(ctx, "merge-base", s.before, s.target.OID); er != nil {
			return result, er
		}
	}
	if q.Operation == "rebase" {
		if s.plan, er = s.planRebase(); er != nil {
			return result, er
		}
		args := []string{"rev-list", "--merges", s.before, "--not", s.plan.UpstreamOID}
		if s.plan.ForkOID != "" {
			args = append(args, s.plan.ForkOID)
		}
		merges, er := e.Git.Text(ctx, args...)
		if er != nil {
			return result, er
		}
		if merges != "" {
			return result, bad("Rebasing merge commits is unsupported")
		}
	}
	if er = s.startJournal(); er != nil {
		return result, er
	}
	return s.execute(false)
}
func validateRequest(q Request) error {
	if q.Operation != "merge" && q.Operation != "rebase" && q.Operation != "cherry-pick" {
		return bad("Operation must be rebase, merge or cherry-pick")
	}
	if e := q.Commit.Validate(); e != nil {
		return e
	}
	if e := q.Rebase.Validate(); e != nil {
		return e
	}
	if q.Continue {
		if q.Target != "" || q.Onto != nil || q.Commit.Present() || q.Rebase.Present() || q.MergeFF != "" || q.Rerere.Present() {
			return bad("Continuation does not accept initial integration options")
		}
		return nil
	}
	if q.Target == "" {
		return bad("An integration target is required")
	}
	if q.Operation != "rebase" && (q.Onto != nil || q.Rebase.Present()) {
		return bad("Rebase options are only supported for rebase")
	}
	if q.Rebase.KeepBase == options.Enabled && q.Onto != nil {
		return bad("--keep-base and --onto cannot be used together")
	}
	if q.Operation != "merge" && (q.Commit.Message != nil || q.Commit.Log != nil || q.Commit.Cleanup != "" || q.Commit.VerifySignatures.Present() || q.MergeFF != "") {
		return bad("Messages, log, cleanup, signature verification and fast-forward policy are only supported for merge")
	}
	if q.MergeFF != "" && q.MergeFF != "ff" && q.MergeFF != "no-ff" && q.MergeFF != "ff-only" {
		return bad("Invalid merge fast-forward policy")
	}
	if q.Validation != nil && len(q.Validation) == 0 {
		return bad("Validation executable is required")
	}
	return nil
}
func (s *session) text(args ...string) (string, error) { return s.engine.Git.Text(s.ctx, args...) }
func (s *session) admit() (string, error) {
	g := s.engine.Git
	r, e := g.Run(s.ctx, "symbolic-ref", "-q", "HEAD")
	if e != nil {
		return "", e
	}
	ref := strings.TrimSpace(string(r.Stdout))
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", bad("A local branch must be checked out")
	}
	active, e := g.Active(s.ctx)
	if e != nil {
		return "", e
	}
	if active {
		return "", bad("A Git operation is already in progress")
	}
	stages, e := g.Conflicts(s.ctx)
	if e != nil {
		return "", e
	}
	if len(stages) > 0 {
		return "", bad("Unmerged index entries require manual resolution")
	}
	r, e = g.Checked(s.ctx, "status", "--porcelain", "--untracked-files=all")
	if e != nil {
		return "", e
	}
	if len(r.Stdout) > 0 {
		return "", bad("Repository must be clean before integration")
	}
	for _, identity := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, e = g.Checked(s.ctx, "var", identity); e != nil {
			return "", e
		}
	}
	return strings.TrimPrefix(ref, "refs/heads/"), nil
}
func (s *session) pin(revision string, includeCurrent bool) (resolver.Pin, error) {
	oid, e := s.engine.Git.CommitOID(s.ctx, revision)
	if e != nil {
		return resolver.Pin{}, e
	}
	p := resolver.Pin{OID: oid}
	r, e := s.engine.Git.Run(s.ctx, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", revision)
	if e != nil {
		return p, e
	}
	ref := strings.TrimSpace(string(r.Stdout))
	if strings.HasPrefix(ref, "refs/") && (includeCurrent || ref != "refs/heads/"+s.branch) {
		p.Reference = ref
	}
	return p, nil
}
func (s *session) verifyPins() error {
	for _, p := range []resolver.Pin{s.target, s.onto} {
		if p.Reference != "" {
			oid, e := s.engine.Git.CommitOID(s.ctx, p.Reference)
			if e != nil {
				return e
			}
			if oid != p.OID {
				return stopped("Input reference changed during integration")
			}
		}
	}
	return nil
}
func (s *session) startJournal() error {
	common, e := s.engine.Git.CommonDir(s.ctx)
	if e != nil {
		return e
	}
	gitdir, e := s.engine.Git.GitDir(s.ctx)
	if e != nil {
		return e
	}
	view := map[string]any{"operation": s.request.Operation, "branch": s.branch, "before_oid": s.before, "target": s.request.Target, "target_oid": s.target.OID, "target_ref": nullable(s.target.Reference), "worktree": s.engine.Git.Dir, "git_directory": gitdir, "common_git_directory": common, "conflicts": []any{}, "reviews": []any{}, "commit_options": commitMetadata(s.request.Commit), "rebase_options": rebaseMetadata(s.request.Rebase), "rerere_autoupdate": s.request.Rerere.Nullable(), "validation": map[string]any{"command": s.request.Validation, "status": "not_requested"}, "min_confidence": s.engine.Config.MinConfidence, "interactive": s.request.Interactive}
	view["merge_ff"] = nullable(s.request.MergeFF)
	view["provider"] = s.engine.Config.Provider
	view["model"] = s.engine.Config.Model
	if s.request.Onto != nil {
		view["onto"] = *s.request.Onto
		view["onto_oid"] = s.onto.OID
		view["onto_ref"] = nullable(s.onto.Reference)
	}
	if s.plan != nil {
		view["rebase_plan"] = s.plan
	}
	if len(s.request.Validation) > 0 {
		view["validation"] = map[string]any{"command": s.request.Validation, "status": "not_run"}
	}
	j, e := journal.Scratch(common, view, s.engine.Config.ResolvedAPIKey(s.lookupEnv))
	if e != nil {
		return e
	}
	s.journal = j
	s.result = Result{Status: "running", BeforeOID: s.before}
	return nil
}
func (s *session) lookupEnv(key string) (string, bool) {
	if s.engine.Git.Env == nil {
		return os.LookupEnv(key)
	}
	for _, v := range s.engine.Git.Env {
		k, x, ok := strings.Cut(v, "=")
		if ok && k == key {
			return x, true
		}
	}
	return "", false
}
func (s *session) execute(resumed bool) (result Result, err error) {
	defer s.journal.Close()
	defer func() {
		if x := recover(); x != nil {
			_ = s.journal.Finish(stopped("Unexpected engine failure"))
			panic(x)
		}
		if s.capture != nil {
			if e := sequence.Verify(*s.capture); e != nil {
				err = errors.Join(err, stopped(e.Error()))
			}
		}
		if errors.Is(err, context.Canceled) {
			s.journal.View["status"] = "interrupted"
		}
		if e := s.journal.Finish(err); e != nil {
			err = errors.Join(err, e)
			// A rename may have succeeded before a directory sync failed.
			// Best effort must revoke any visible success claim; the original
			// required-publication error still fails the operation.
			_ = s.journal.Finish(err)
		}
		if err != nil {
			s.result.Status = "failed"
			if errors.Is(err, context.Canceled) {
				s.result.Status = "interrupted"
			}
		} else {
			s.result.Status = "succeeded"
		}
		if err != nil {
			err = failure.Wrap(failure.Execution, string(s.journal.Redact([]byte(err.Error()))), err)
		}
		if !s.journal.Persistent() {
			s.result.Directory = ""
		}
		result = s.result
	}()
	if !s.result.Integrated {
		if err = s.integrate(resumed); err != nil {
			return s.result, err
		}
	}
	if len(s.request.Validation) > 0 {
		if err = s.validate(); err != nil {
			validation, _ := s.journal.View["validation"].(map[string]any)
			s.result.FinalValidationFailed = validation["status"] != "passed"
			return s.result, err
		}
	}
	if err = s.verifyResult(); err != nil {
		return s.result, err
	}
	return s.result, nil
}
func (s *session) completed() error {
	oid, e := s.engine.Git.CommitOID(s.ctx, "HEAD")
	if e != nil {
		return e
	}
	s.result.Integrated = true
	s.result.ResultOID = oid
	s.journal.View["integrated"] = true
	s.journal.View["result_oid"] = oid
	return s.journal.Save()
}
func (s *session) verifyResult() error {
	oid, e := s.engine.Git.CommitOID(s.ctx, "HEAD")
	if e != nil {
		return e
	}
	if oid != s.result.ResultOID {
		return stopped("Validation changed HEAD")
	}
	branch, e := s.admit()
	if e != nil {
		return stopped("Git result is not a clean completed checkout: " + e.Error())
	}
	if branch != s.branch {
		return stopped("Checked-out branch changed during integration")
	}
	branchOID, e := s.engine.Git.BranchOID(s.ctx, s.branch)
	if e != nil {
		return e
	}
	if branchOID != s.result.ResultOID {
		return stopped("Current branch changed during final verification")
	}
	if e = s.verifyPins(); e != nil {
		return e
	}
	if s.request.Operation == "cherry-pick" {
		line, e := s.text("rev-list", "--parents", "-n", "1", oid)
		if e != nil {
			return e
		}
		parents := strings.Fields(line)
		if len(parents) != 2 || parents[1] != s.before {
			return stopped("Cherry-pick result is not one commit on the original branch")
		}
		return nil
	}
	base := s.target.OID
	if s.plan != nil {
		base = s.plan.BaseOID
	}
	for _, parent := range []string{base, s.before} {
		if s.request.Operation == "rebase" && parent == s.before {
			continue
		}
		r, e := s.engine.Git.Run(s.ctx, "merge-base", "--is-ancestor", parent, oid)
		if e != nil {
			return e
		}
		if r.ExitCode != 0 {
			return stopped("Integration result does not contain required pinned ancestry")
		}
	}
	return nil
}
func (s *session) validate() error {
	v := map[string]any{"command": s.request.Validation, "status": "running"}
	s.journal.View["validation"] = v
	if e := s.journal.Save(); e != nil {
		v["status"] = "not_run"
		return e
	}
	stdout := newDiagnostic(s.journal, "validation.stdout")
	stderr := newDiagnostic(s.journal, "validation.stderr")
	s.engine.Report.SetSecret(s.engine.Config.ResolvedAPIKey(s.lookupEnv))
	// Redact literal arguments before shell-style quoting can split a secret.
	displayArgs := make([]string, len(s.request.Validation))
	for i, arg := range s.request.Validation {
		displayArgs[i] = string(s.journal.Redact([]byte(arg)))
	}
	command := terminal.Command(displayArgs)
	s.engine.Report.Info("git-ez: Validating resolution")
	s.engine.Report.Info("  Running: %s", command)
	stopProgress := s.engine.Report.Begin(s.ctx, "Check in progress")
	defer stopProgress()
	r, e := runValidation(s.ctx, s.engine.Git, s.request.Validation, s.engine.Config.TimeoutDuration(), stdout, stderr)
	stopProgress()
	for _, stream := range []string{"stdout", "stderr"} {
		d := stdout
		if stream == "stderr" {
			d = stderr
		}
		available := d.finish(r.Started)
		v[stream+"_available"] = available
		v[stream+"_preview"] = d.preview.buffer.String()
		v[stream+"_preview_truncated"] = d.preview.truncated
		if d.err != nil {
			s.diagnosticWarning(v, stream)
		}
	}
	if r.Started {
		v["returncode"] = r.ExitCode
	}
	v["stdout_truncated"] = r.StdoutTruncated
	v["stderr_truncated"] = r.StderrTruncated
	v["status"] = "passed"
	if e != nil || r.ExitCode != 0 {
		v["status"] = "failed"
	}
	if r.TimedOut {
		v["status"] = "timed_out"
	}
	if v["status"] == "passed" {
		s.engine.Report.Info("git-ez: Check passed.")
	}
	if v["status"] != "passed" {
		s.engine.Report.Diagnostic("git-ez: Validation failed.")
		s.engine.Report.Diagnostic("  Command: %s", command)
		if r.Started && r.ExitCode >= 0 {
			s.engine.Report.Diagnostic("  Exit code: %d", r.ExitCode)
		}
		destination := s.engine.Err
		if destination == nil {
			destination = io.Discard
		}
		for _, stream := range []string{"stdout", "stderr"} {
			if v[stream+"_available"] != true {
				if preview, _ := v[stream+"_preview"].(string); preview != "" {
					fmt.Fprintf(destination, "Validation %s (incomplete evidence):\n%s\n", stream, review.SafeText(preview))
				}
				continue
			}
			raw, readErr := s.journal.Window("validation."+stream, 0, 8193)
			if readErr == nil && len(raw) > 0 {
				fmt.Fprintf(destination, "Validation %s:\n%s\n", stream, review.SafeText(string(raw[:min(len(raw), 8192)])))
				if len(raw) > 8192 {
					fmt.Fprintln(destination, "[Output preview truncated; rerun the validation command for complete output.]")
				}
			}
		}
	}
	if v["status"] != "passed" && s.journal.Persistent() {
		s.engine.Report.Diagnostic("  Retained diagnostics (may be incomplete): %s", s.journal.Directory)
	}
	if saveErr := s.journal.Save(); saveErr != nil {
		return errors.Join(e, saveErr)
	}
	if e != nil {
		return e
	}
	if r.ExitCode != 0 {
		return stopped(fmt.Sprintf("Validation failed with code %d", r.ExitCode))
	}
	return nil
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func commitMetadata(c options.Commit) map[string]any {
	return map[string]any{"message": c.Message, "gpg_sign": c.Signing.Nullable(), "signoff": c.Signoff.Nullable(), "verify_signatures": c.VerifySignatures.Nullable(), "log": c.Log, "cleanup": nullable(c.Cleanup)}
}
func rebaseMetadata(r options.Rebase) map[string]any {
	return map[string]any{"empty": nullable(r.Empty), "keep_empty": r.KeepEmpty.Nullable(), "reapply_cherry_picks": r.ReapplyCherryPicks.Nullable(), "force_rebase": r.ForceRebase.Nullable(), "autosquash": r.Autosquash.Nullable(), "keep_base": r.KeepBase == options.Enabled, "fork_point": r.ForkPoint.Nullable()}
}
