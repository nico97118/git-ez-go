// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nico97118/git-ez-go/internal/conflictstate"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/process"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/review"
)

func (s *session) proposalFiles(stages git.Conflicts, baseline map[string]*fingerprint, extra []string) ([]string, []fileEvidence, error) {
	changed, e := s.changed()
	if e != nil {
		return nil, nil, e
	}
	all := append(changed, extra...)
	for name := range stages {
		all = append(all, name)
	}
	for name := range baseline {
		all = append(all, name)
	}
	selected := []string{}
	files := []fileEvidence{}
	forced := map[string]bool{}
	for _, name := range extra {
		forced[name] = true
	}
	for _, name := range unique(all) {
		old, was := baseline[name]
		now, e := s.fingerprint(name)
		_, conflict := stages[name]
		if !conflict && !forced[name] && was && e == nil && same(old, now) {
			continue
		}
		if e != nil {
			return nil, nil, e
		}
		if !conflict && !forced[name] && !was && now == nil {
			continue
		}
		path, e := s.engine.Git.SafePath(name)
		if e != nil {
			return nil, nil, e
		}
		raw, e := journal.Read(path, int64(s.engine.Config.MaxFileBytes))
		var content *string
		if !os.IsNotExist(e) {
			if e != nil {
				return nil, nil, e
			}
			if !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) || markers.Match(raw) {
				return nil, nil, stopped("Non-text content, conflict markers or oversized proposal: " + name)
			}
			text := string(raw)
			content = &text
		}
		selected = append(selected, name)
		files = append(files, fileEvidence{name, content, now})
	}
	return selected, files, nil
}
func (s *session) checkFiles(files []fileEvidence) error {
	for _, f := range files {
		now, e := s.fingerprint(f.Path)
		if e != nil {
			return e
		}
		if !same(now, f.Fingerprint) {
			return stopped("Proposed contents changed before or during staging")
		}
	}
	return nil
}

type reviewDigest struct {
	Hash  [32]byte
	Bytes int64
}

func (s *session) captureReview(j *journal.Journal, decision resolver.Decision, selected []string, files []fileEvidence) (func(io.Writer) error, map[string]reviewDigest, error) {
	if e := s.proposalDiff(j, selected); e != nil {
		return nil, nil, e
	}
	names := []string{"worktree.diff", "files.json", "decision.json"}
	manifests := []map[string]any{}
	for i, f := range files {
		record := map[string]any{"path": f.Path, "fingerprint": f.Fingerprint, "state": "deleted"}
		manifests = append(manifests, record)
		if f.Content != nil {
			name := fmt.Sprintf("file-%04d.txt", i+1)
			if e := j.Write(name, []byte(*f.Content)); e != nil {
				return nil, nil, e
			}
			record["state"] = "file"
			record["artifact"] = name
			record["truncated"] = false
			record["size"] = len(*f.Content)
			record["recorded_bytes"] = len(j.Redact([]byte(*f.Content)))
			record["redacted"] = string(j.Redact([]byte(*f.Content))) != *f.Content
			names = append(names, name)

		}
	}
	if e := j.JSON("files.json", manifests); e != nil {
		return nil, nil, e
	}

	hashes := map[string]reviewDigest{}
	for _, name := range names {
		hash, size, e := j.Digest(name)
		if e != nil {
			return nil, nil, e
		}
		hashes[name] = reviewDigest{Hash: hash, Bytes: size}
	}
	render := func(out io.Writer) error {
		for _, name := range names {
			if _, e := fmt.Fprintln(out, name+":"); e != nil {
				return e
			}
			source, e := j.OpenEvidence(name)
			if e != nil {
				return e
			}
			e = errors.Join(review.WriteSafe(out, source), source.Close())
			if e != nil {
				return e
			}
			if _, e := fmt.Fprintln(out); e != nil {
				return e
			}
		}
		_, e := fmt.Fprintln(out, review.SafeText(string(j.Redact([]byte("Agent rationale, reported checks and uncertainties: "+decision.Rationale)))))
		return e
	}
	return render, hashes, nil
}

func verifyReviewEvidence(j *journal.Journal, hashes map[string]reviewDigest) error {
	for name, old := range hashes {
		hash, size, e := j.Digest(name)
		if e != nil {
			return e
		}
		if size != old.Bytes || hash != old.Hash {
			return stopped("Proposal evidence changed during review")
		}
	}
	return j.Check()
}
func baselineEqual(a, b map[string]*fingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for n, v := range a {
		other, ok := b[n]
		if !ok || !same(v, other) {
			return false
		}
	}
	return true
}

func (s *session) reviewConflicts(stop resolver.Stop, stages git.Conflicts, round int, baseline map[string]*fingerprint, records map[string]map[string]any, state *conflictstate.State, head, branch string, index []byte, identity *position) error {
	if s.engine.Resolver == nil {
		return stopped("No resolver is configured")
	}
	if s.reviewInput == nil {
		s.reviewInput = review.NewInput(s.engine.In)
	}
	feedback := ""
	previous := []resolver.ProposalEvidence{}
	automatic := 0
	for {
		if e := s.ctx.Err(); e != nil {
			return e
		}
		if e := state.Restore(s.ctx); e != nil {
			return e
		}
		automatic++
		number := len(s.journal.View["reviews"].([]any)) + 1
		name := fmt.Sprintf("proposal-%04d", number)
		j, e := s.journal.Child(name)
		if e != nil {
			return e
		}
		// Close each proposal's handle before the next attempt; retained files remain.
		err := func() (attemptErr error) {
			defer j.Close()
			record := map[string]any{"id": journal.ID(), "round": round, "attempt": number, "status": "running", "application": "not_staged", "artifacts": name, "feedback": feedback}
			ids := []string{}
			stopID := ""
			for _, c := range stop.Conflicts {
				item := records[c.Path]
				ids = append(ids, item["id"].(string))
				stopID, _ = item["stop_id"].(string)
			}
			record["diff_base_oid"] = head
			record["conflict_ids"] = ids
			record["stop_id"] = stopID
			s.journal.View["reviews"] = appendRecord(s.journal.View["reviews"], record)
			defer func() {
				if attemptErr != nil {
					record["status"] = "failed"
					if !cleanupUncertain(attemptErr) {
						if captureErr := s.captureFailedAttempt(j, stages, baseline); captureErr != nil {
							record["capture_error"] = "Failed proposal capture unavailable"
							attemptErr = errors.Join(attemptErr, captureErr)
						}
					}
					if s.ctx.Err() != nil {
						record["status"] = "interrupted"
					}
					record["error"] = string(s.journal.Redact([]byte(attemptErr.Error())))
					attemptErr = errors.Join(attemptErr, s.journal.Save())
				}
			}()
			if e = s.journal.Save(); e != nil {
				return e
			}
			current := stop
			current.Review = &resolver.Review{Round: number, Feedback: feedback, Previous: append([]resolver.ProposalEvidence{}, previous...)}
			retain := stop.Retain
			current.Retain = func(ev resolver.Evidence) error {
				if err := retain(ev); err != nil {
					return err
				}
				sessions := s.journal.View["sessions"].([]any)
				last := sessions[len(sessions)-1].(map[string]any)
				last["attempt"] = number
				last["stop_id"] = stopID
				record["session"] = last["artifacts"]
				return s.journal.Save()
			}
			s.result.ResolverCalls++
			decision, resolveErr := s.engine.Resolver.Resolve(s.ctx, current)
			record["resolution"] = decision
			if cleanupUncertain(resolveErr) {
				captureErr := j.JSON("decision.json", decision)
				return errors.Join(resolveErr, captureErr)
			}
			if e = j.JSON("decision.json", decision); e != nil {
				return e
			}
			if e = s.journal.Save(); e != nil {
				return e
			}
			// Capture even failed attempts where safe, before restoration.
			capture := func() (selected []string, files []fileEvidence, display func(io.Writer) error, hashes map[string]reviewDigest, captureErr error) {
				extra, e := state.Changed()
				if e != nil {
					captureErr = e
					return
				}
				selected, files, captureErr = s.proposalFiles(stages, baseline, extra)
				if captureErr != nil {
					return
				}
				display, hashes, captureErr = s.captureReview(j, decision, selected, files)
				return
			}
			if s.ctx.Err() != nil || resolveErr != nil {
				_, _, _, _, captureErr := capture()
				if captureErr != nil {
					record["capture_error"] = "Proposal capture unavailable"
				}
				if resolveErr == nil {
					resolveErr = s.ctx.Err()
				}
				return failure.Wrap(failure.Execution, "Resolver failed; proposal retained before restoration", resolveErr)
			}
			if e = decision.Validate(); e != nil {
				return e
			}
			if e = s.guard(head, branch, index, identity); e != nil {
				return e
			}
			if _, _, e = s.proposalFiles(stages, baseline, nil); e != nil {
				return e
			}
			failed := false
			diagnostic := ""
			if len(s.request.Validation) > 0 {
				validationErr := s.validate()
				record["validation"] = maps.Clone(s.journal.View["validation"].(map[string]any))
				if cleanupUncertain(validationErr) {
					return validationErr
				}
				if s.ctx.Err() != nil {
					return s.ctx.Err()
				}
				if validationErr != nil {
					// Publication errors cannot be treated as an ordinary check failure.
					if failure.IsKind(validationErr, failure.Audit) {
						return validationErr
					}
					failed = true
					diagnostic = validationErr.Error()
					for _, stream := range []string{"stdout", "stderr"} {
						var raw []byte
						var err error
						if s.journal.View["validation"].(map[string]any)[stream+"_available"] == true {
							raw, err = s.journal.Window("validation."+stream, 0, int64(s.engine.Config.ToolOutputBytes))
						} else {
							err = fmt.Errorf("Current validation diagnostic unavailable")
						}
						if err != nil {
							preview, _ := s.journal.View["validation"].(map[string]any)[stream+"_preview"].(string)
							diagnostic += "\nValidation output incomplete.\n" + preview
						} else {
							diagnostic += "\n" + string(raw)
							if info, err := os.Stat(filepath.Join(s.journal.Directory, "validation."+stream)); err == nil && info.Size() > int64(len(raw)) {
								diagnostic += "\n[Validation diagnostic truncated]"
							}
						}
					}
					diagnostic = string(s.journal.Redact([]byte(diagnostic)))
				}
				// Per-attempt streams remain distinct even when the next validation runs.
				for _, stream := range []string{"stdout", "stderr"} {
					if s.journal.View["validation"].(map[string]any)[stream+"_available"] != true {
						continue
					}
					if e = s.journal.CopyTo(j, "validation."+stream); e != nil {
						v := record["validation"].(map[string]any)
						v[stream+"_available"] = false
						s.diagnosticWarning(v, stream)
					}
				}
				record["validation_diagnostic"] = diagnostic
			}
			if e = s.guard(head, branch, index, identity); e != nil {
				return e
			}
			if e = state.VerifyControl(); e != nil {
				return e
			}
			selected, files, display, hashes, e := capture()
			if e != nil {
				return e
			}
			record["paths"] = selected
			record["status"] = "proposed"
			if e = s.journal.Save(); e != nil {
				return e
			}
			history := resolver.ProposalEvidence{Directory: j.Directory, Feedback: feedback, ValidationDiagnostic: diagnostic, Decision: &decision}
			if failed && automatic < s.engine.Config.ValidationAttempts {
				record["status"] = "check_failed"
				record["decision"] = "automatic_retry"
				feedback = "Previous check failed. Correct the resolution:\n" + diagnostic
				previous = append(previous, history)
				return s.journal.Save()
			}
			before, e := s.baseline()
			if e != nil {
				return e
			}
			tracked, e := state.Changed()
			if e != nil {
				return e
			}
			action, user, e := review.Prompt(s.ctx, s.reviewInput, s.engine.Out, review.Proposal{Render: display, Confidence: decision.Confidence, ValidationFailed: failed, Diagnostic: diagnostic})
			record["decision"] = action
			record["user_feedback"] = string(s.journal.Redact([]byte(user)))
			if saveErr := s.journal.Save(); saveErr != nil {
				return saveErr
			}
			if e != nil {
				if s.ctx.Err() != nil {
					return s.ctx.Err()
				}
				return stopped("Cannot read review input; proposal and Git operation preserved")
			}
			if action == "stop" {
				return stopped("Review stopped; original unresolved conflict restored")
			}
			if e = s.guard(head, branch, index, identity); e != nil {
				return e
			}
			if e = state.VerifyControl(); e != nil {
				return e
			}
			if e = s.checkFiles(files); e != nil {
				return e
			}
			after, e := s.baseline()
			if e != nil {
				return e
			}
			changes, e := state.Changed()
			if e != nil {
				return e
			}
			currentPaths, _, err := s.proposalFiles(stages, baseline, changes)
			if err != nil {
				return err
			}
			if strings.Join(selected, "\x00") != strings.Join(currentPaths, "\x00") {
				return stopped("Proposal paths changed during review")
			}
			if !baselineEqual(before, after) || strings.Join(tracked, "\x00") != strings.Join(changes, "\x00") {
				return stopped("Worktree changed during review; review no longer applies")
			}
			if e = verifyReviewEvidence(j, hashes); e != nil {
				return e
			}
			if action == "accept" && !failed {
				if decision.Confidence < s.engine.Config.MinConfidence {
					return stopped("Resolver confidence below threshold")
				}
				if e = s.stageApproved(selected, files); e != nil {
					return e
				}
				staged, err := s.engine.Git.Checked(s.ctx, "ls-files", "--stage", "-z")
				if err != nil {
					return err
				}
				if e = s.guard(head, branch, staged.Stdout, identity); e != nil {
					return e
				}
				if e = state.VerifyControl(); e != nil {
					return e
				}
				record["status"] = "applied"
				record["application"] = "staged"
				for _, item := range records {
					item["status"] = "applied"
					item["application"] = "staged"
					item["resolution"] = decision
					item["proposal"] = name
				}
				if e = s.journal.Save(); e != nil {
					return e
				}
				feedback = ""
				return nil
			}
			automatic = 0
			feedback = string(s.journal.Redact([]byte(user)))
			history.UserFeedback = feedback
			previous = append(previous, history)
			return nil
		}()
		if err != nil {
			return err
		}
		last := s.journal.View["reviews"].([]any)
		if last[len(last)-1].(map[string]any)["application"] == "staged" {
			return nil
		}
	}
}

func (s *session) stageApproved(paths []string, files []fileEvidence) (err error) {
	if err = s.checkFiles(files); err != nil {
		return err
	}
	g := s.engine.Git
	tags, e := g.Checked(s.ctx, append([]string{"ls-files", "-v", "-z", "--"}, paths...)...)
	if e != nil {
		return e
	}
	assumed, skipped := []string{}, []string{}
	for _, entry := range bytes.Split(tags.Stdout, []byte{0}) {
		if len(entry) < 3 {
			continue
		}
		tag := entry[0]
		name := string(entry[2:])
		if tag >= 'a' && tag <= 'z' {
			assumed = append(assumed, name)
		}
		if tag == 's' || tag == 'S' {
			skipped = append(skipped, name)
		}
	}
	defer func() {
		ctx := context.WithoutCancel(s.ctx)
		remaining, e := g.Checked(ctx, append([]string{"ls-files", "-z", "--"}, paths...)...)
		if e != nil {
			err = errors.Join(err, e)
			return
		}
		present := map[string]bool{}
		for _, n := range bytes.Split(remaining.Stdout, []byte{0}) {
			present[string(n)] = true
		}
		for flag, names := range map[string][]string{"assume-unchanged": assumed, "skip-worktree": skipped} {
			selected := []string{}
			for _, n := range names {
				if present[n] {
					selected = append(selected, n)
				}
			}
			if len(selected) > 0 {
				_, e = g.Checked(ctx, append([]string{"update-index", "--" + flag, "--"}, selected...)...)
				err = errors.Join(err, e)
			}
		}
	}()
	for flag, names := range map[string][]string{"assume-unchanged": assumed, "skip-worktree": skipped} {
		if len(names) > 0 {
			if _, err = g.Checked(s.ctx, append([]string{"update-index", "--no-" + flag, "--"}, names...)...); err != nil {
				return err
			}
		}
	}
	if _, err = g.Checked(s.ctx, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	entries, e := g.Checked(s.ctx, append([]string{"ls-files", "--stage", "-z", "--"}, paths...)...)
	if e != nil {
		return e
	}
	indexed := map[string][]string{}
	for _, entry := range bytes.Split(entries.Stdout, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(entry, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || fields[2] != "0" {
			return stopped("Approved resolution still has unmerged index entries")
		}
		indexed[string(name)] = fields
	}
	for _, f := range files {
		entry, exists := indexed[f.Path]
		if f.Fingerprint == nil {
			if exists {
				return stopped("Approved deletion was not staged")
			}
			continue
		}
		oid, e := g.Text(s.ctx, "hash-object", "--path="+f.Path, "--", f.Path)
		if e != nil {
			return e
		}
		if !exists || entry[1] != oid {
			return stopped("Approved content was not staged")
		}
		mode := "100644"
		if f.Fingerprint.Mode&0100 != 0 {
			mode = "100755"
		}
		if entry[0] != mode {
			return stopped("Approved executable mode was not staged; inspect core.fileMode and resolve manually")
		}
	}
	return s.checkFiles(files)
}

// captureFailedAttempt preserves safe, bounded current evidence without treating
// invalid content or an incomplete resolver decision as an approvable proposal.
func (s *session) captureFailedAttempt(j *journal.Journal, stages git.Conflicts, baseline map[string]*fingerprint) error {
	copy := *s
	diagnosticCtx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), min(s.engine.Config.TimeoutDuration(), 5*time.Second))
	defer cancel()
	copy.ctx = diagnosticCtx
	s = &copy

	if _, e := os.Lstat(filepath.Join(j.Directory, "files.json")); e == nil {
		return nil
	}
	names := []string{}
	for name := range stages {
		names = append(names, name)
	}
	for name := range baseline {
		names = append(names, name)
	}
	if changed, e := s.changed(); e == nil {
		names = append(names, changed...)
	}
	records := []map[string]any{}
	for i, name := range unique(names) {
		record := map[string]any{"path": name}
		records = append(records, record)
		path, e := s.engine.Git.SafePath(name)
		if e != nil {
			record["capture_error"] = "Unsafe file path"
			continue
		}
		raw, e := journal.Read(path, int64(s.engine.Config.MaxFileBytes))
		if os.IsNotExist(e) {
			record["state"] = "deleted"
			continue
		}
		if e != nil {
			record["capture_error"] = "Incomplete or unsupported capture"
			record["truncated"] = true
			continue
		}
		artifact := fmt.Sprintf("file-%04d.txt", i+1)
		if e = j.Write(artifact, raw); e != nil {
			return e
		}
		record["state"] = "file"
		record["artifact"] = artifact
		record["size"] = len(raw)
		record["truncated"] = false
	}
	if _, e := os.Lstat(filepath.Join(j.Directory, "worktree.diff")); os.IsNotExist(e) {
		diffErr := s.proposalDiff(j, unique(names))
		if cleanupUncertain(diffErr) {
			return diffErr
		}
	}
	return j.JSON("files.json", records)
}

// proposalDiff streams complete mandatory proposal evidence, independently of
// model context capacity. IO/resource failures prevent approval.
func (s *session) proposalDiff(j *journal.Journal, paths []string) error {
	out, e := j.Capture("worktree.diff")
	if e != nil {
		return e
	}
	defer out.Close()
	diagnostics := &validationCapture{limit: 8192}
	args := append([]string{"git", "--no-pager", "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--"}, paths...)
	env := s.engine.Git.Env
	if env == nil {
		env = os.Environ()
	}
	result, e := process.Run(s.ctx, process.Request{Args: args, Dir: s.engine.Git.Dir, Env: git.Environment(env, true), Stdout: out, Stderr: diagnostics, Timeout: s.engine.Config.TimeoutDuration()})
	e = errors.Join(e, out.Close())
	if e != nil {
		return e
	}
	if result.ExitCode != 0 {
		return stopped("Cannot capture proposal diff")
	}
	return nil
}
