// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/process"
	"github.com/nico97118/git-ez-go/internal/sequence"
)

type Plan struct {
	BaseOID          string `json:"base_oid"`
	UpstreamOID      string `json:"upstream_oid"`
	SelectionOID     string `json:"selection_oid"`
	BaseSource       string `json:"base_source"`
	Reapply          bool   `json:"reapply_cherry_picks"`
	ForkEnabled      bool   `json:"fork_enabled"`
	ForkOID          string `json:"fork_oid,omitempty"`
	ForkRef          string `json:"fork_ref,omitempty"`
	ForkDiagnostic   string `json:"-"`
	ForkStatus       string `json:"fork_status"`
	RequiresSequence bool   `json:"requires_sequence"`
}

func (s *session) planRebase() (*Plan, error) {
	q := s.request
	p := &Plan{BaseOID: s.target.OID, UpstreamOID: s.target.OID, BaseSource: "upstream", ForkStatus: "disabled"}
	if s.onto.OID != "" {
		p.BaseOID = s.onto.OID
		p.BaseSource = "onto"
	}
	if q.Rebase.KeepBase == options.Enabled {
		r, e := s.engine.Git.Run(s.ctx, "merge-base", "--all", s.target.OID, s.before)
		if e != nil {
			return nil, e
		}
		bases := strings.Fields(string(r.Stdout))
		if r.ExitCode > 1 || len(bases) != 1 {
			return nil, bad("--keep-base requires exactly one merge base with the upstream")
		}
		p.BaseOID = bases[0]
		p.BaseSource = "merge-base"
	}
	cfg, e := s.engine.Git.Run(s.ctx, "config", "--bool", "--get", "rebase.forkPoint")
	if e != nil {
		return nil, e
	}
	if cfg.ExitCode > 1 {
		return nil, bad("Cannot read rebase.forkPoint")
	}
	p.Reapply = q.Rebase.ReapplyCherryPicks == options.Enabled || (q.Rebase.ReapplyCherryPicks == options.Unspecified && q.Rebase.KeepBase == options.Enabled)
	if q.Rebase.KeepBase == options.Enabled && p.Reapply {
		p.UpstreamOID = p.BaseOID
	}
	if q.Rebase.ForkPoint == options.Enabled {
		p.ForkEnabled = true
		ref, e := s.pin(q.Target, true)
		if e != nil {
			return nil, e
		}
		if ref.Reference == "" {
			return nil, bad("--fork-point requires a named upstream reference")
		}
		p.ForkRef = ref.Reference
		r, e := s.engine.Git.Run(s.ctx, "merge-base", "--fork-point", p.ForkRef, s.before)
		if e != nil {
			return nil, e
		}
		if r.ExitCode > 1 {
			return nil, bad("Cannot determine fork point")
		}
		p.ForkStatus = "fallback"
		p.ForkDiagnostic = strings.TrimSpace(strings.ToValidUTF8(string(r.Stderr), "�"))
		if r.ExitCode == 0 {
			p.ForkOID, e = s.engine.Git.CommitOID(s.ctx, strings.TrimSpace(string(r.Stdout)))
			if e != nil {
				return nil, e
			}
			p.ForkStatus = "found"
		}
	}
	return p, nil
}
func (s *session) selection() error {
	p := s.plan
	p.SelectionOID = p.UpstreamOID
	if p.ForkOID != "" {
		r, e := s.engine.Git.Run(s.ctx, "merge-base", "--is-ancestor", p.UpstreamOID, p.ForkOID)
		if e != nil {
			return e
		}
		if r.ExitCode == 0 {
			p.SelectionOID = p.ForkOID
		} else {
			r, e = s.engine.Git.Run(s.ctx, "merge-base", "--is-ancestor", p.ForkOID, p.UpstreamOID)
			if e != nil {
				return e
			}
			if r.ExitCode != 0 {
				tree, e := s.text("rev-parse", p.UpstreamOID+"^{tree}")
				if e != nil {
					return e
				}
				p.SelectionOID, e = s.text("-c", "commit.gpgSign=false", "commit-tree", tree, "-p", p.UpstreamOID, "-p", p.ForkOID, "-m", "git-ez pinned rebase selection")
				if e != nil {
					return e
				}
			}
		}
	}
	p.RequiresSequence = p.ForkOID != "" && p.SelectionOID != p.UpstreamOID && p.BaseOID == p.ForkOID
	return s.journal.Save()
}
func (s *session) setupRerere() error {
	if s.request.Rerere.Present() {
		s.rerere = s.request.Rerere == options.Enabled
		value := "false"
		if s.rerere {
			value = "true"
		}
		s.prefix = []string{"-c", "rerere.autoupdate=" + value}
	} else {
		r, e := s.engine.Git.Run(s.ctx, "config", "--bool", "--get", "rerere.autoupdate")
		if e != nil {
			return e
		}
		s.rerere = strings.TrimSpace(string(r.Stdout)) == "true"
	}
	s.journal.View["rerere_autoupdate_effective"] = s.rerere
	return s.journal.Save()
}
func (s *session) step(editor string, files []*os.File, args ...string) (process.Result, error) {
	g := s.engine.Git
	prior, e := g.Run(s.ctx, "rev-parse", "--verify", "AUTO_MERGE^{tree}")
	if e != nil {
		return process.Result{}, e
	}
	r, e := g.RunWithFiles(s.ctx, files, editor, append(append([]string{}, s.prefix...), args...)...)
	if e != nil {
		return r, e
	}
	s.autoResolved = false
	if r.ExitCode != 0 {
		// A failed commit cannot be an eligible replay stop, even if a failing
		// hook manufactured unmerged index entries. Never retry such a hook.
		if len(args) > 0 && (args[0] == "commit" || (args[0] == "merge" && len(args) > 1 && args[1] == "--continue")) {
			return r, stopped("Native Git commit/continuation failed: " + strings.TrimSpace(strings.ToValidUTF8(string(r.Stderr), "�")))
		}
		stages, e := g.Conflicts(s.ctx)
		if e != nil {
			return r, e
		}
		current, e := g.Run(s.ctx, "rev-parse", "--verify", "AUTO_MERGE^{tree}")
		if e != nil {
			return r, e
		}
		active, e := g.Active(s.ctx)
		if e != nil {
			return r, e
		}
		s.autoResolved = s.rerere && len(current.Stdout) > 0 && string(current.Stdout) != string(prior.Stdout) && active
		if (len(stages) == 0 || !active) && !s.autoResolved {
			return r, stopped("Native Git command failed: " + strings.TrimSpace(strings.ToValidUTF8(string(r.Stderr), "�")))
		}
	}
	return r, nil
}
func (s *session) integrate(resumed bool) error {
	if e := s.setupRerere(); e != nil {
		return e
	}
	q := s.request
	g := s.engine.Git
	var r process.Result
	var e error
	if resumed {
		r.ExitCode = 1
	} else {
		branch, err := s.admit()
		if err != nil {
			return err
		}
		before, err := g.BranchOID(s.ctx, s.branch)
		if err != nil {
			return err
		}
		if branch != s.branch || before != s.before {
			return stopped("Current branch changed before integration")
		}
		if e = s.verifyPins(); e != nil {
			return e
		}
		switch q.Operation {
		case "merge":
			args := []string{"merge", "--no-autostash", "--no-edit"}
			if q.MergeFF != "" {
				args = append(args, "--"+q.MergeFF)
			}
			args = append(args, q.Commit.Flags(true, false)...)
			args = append(args, s.target.OID)
			r, e = s.step("", nil, args...)
		case "cherry-pick":
			args := append([]string{"cherry-pick"}, q.Commit.Flags(false, false)...)
			r, e = s.step("", nil, append(args, s.target.OID)...)
		case "rebase":
			r, e = s.initialRebase()
		}
		if e != nil {
			if r.Started && r.ExitCode == 0 {
				active, checkErr := g.Active(s.ctx)
				if checkErr == nil && !active {
					s.result.Integrated = true
					s.result.ResultOID, _ = g.CommitOID(s.ctx, "HEAD")
					s.journal.View["integrated"] = true
					s.journal.View["result_oid"] = s.result.ResultOID
				}
			}
			return e
		}
	}
	rounds := 0
	var lastStop *position
	for r.ExitCode != 0 {
		if q.Operation == "rebase" {
			p := s.position()
			if p == nil {
				return stopped("Git stopped without a verifiable rebase replay position")
			}
			if lastStop != nil && (p.Backend != lastStop.Backend || p.Step <= lastStop.Step || p.OID == lastStop.OID) {
				return stopped("Native rebase did not reach a genuinely new replay conflict")
			}
			lastStop = p
		}
		stages, err := g.Conflicts(s.ctx)
		if err != nil {
			return err
		}
		if len(stages) == 0 {
			active, err := g.Active(s.ctx)
			if err != nil {
				return err
			}
			if !s.autoResolved || !active {
				return stopped("Git stopped without resolvable conflicts; inspect Git state")
			}
		} else {
			rounds++
			if rounds > s.engine.Config.MaxRounds {
				return stopped("Maximum conflict rounds exceeded")
			}
			if err = s.resolve(stages, rounds); err != nil {
				return err
			}
		}
		remaining, err := g.Conflicts(s.ctx)
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return stopped("Unmerged index entries remain")
		}
		if e = s.verifyPins(); e != nil {
			return e
		}
		if s.capture != nil {
			if e = sequence.Verify(*s.capture); e != nil {
				return e
			}
		}
		if q.Operation == "rebase" && q.Rebase.Empty != "" {
			diff, err := g.Run(s.ctx, "diff", "--cached", "--quiet", "HEAD")
			if err != nil {
				return err
			}
			grouped := false
			if q.Rebase.Autosquash == options.Enabled {
				action, err := s.rebaseAction()
				if err != nil {
					return err
				}
				grouped = action == "fixup" || action == "squash"
			}
			if diff.ExitCode == 0 && !grouped {
				replay, err := g.CommitOID(s.ctx, "REBASE_HEAD")
				if err != nil {
					return err
				}
				s.journal.View["empty_commits"] = appendRecord(s.journal.View["empty_commits"], map[string]any{"commit": replay, "policy": q.Rebase.Empty})
				if err = s.journal.Save(); err != nil {
					return err
				}
				if q.Rebase.Empty == "stop" {
					return stopped("Rebase resolution is empty; use native Git to retain or skip the commit")
				}
				if q.Rebase.Empty == "keep" {
					args := []string{"commit", "--allow-empty", "--allow-empty-message", "--no-edit", "-C", replay}
					args = append(args, q.Commit.Flags(false, false)...)
					if _, err = s.step("", nil, args...); err != nil {
						return err
					}
				}
			}
		}
		switch q.Operation {
		case "cherry-pick":
			oid, err := g.CommitOID(s.ctx, "CHERRY_PICK_HEAD")
			if err != nil || oid != s.target.OID {
				return stopped("Cherry-pick target changed before Git continuation")
			}
			r, e = s.step("", nil, append([]string{"commit", "--no-edit"}, q.Commit.Flags(false, false)...)...)
		case "merge":
			oid, err := g.CommitOID(s.ctx, "MERGE_HEAD")
			if err != nil || oid != s.target.OID {
				return stopped("Merge target changed before Git continuation")
			}
			if len(q.Commit.Flags(false, false)) > 0 {
				log, err := g.Run(s.ctx, "config", "--type=bool-or-int", "--get", "merge.log")
				if err != nil {
					return err
				}
				value := strings.TrimSpace(string(log.Stdout))
				prepared := q.Commit.Log != nil || q.Commit.Cleanup != "" || (value != "" && value != "false" && value != "0")
				r, e = s.step("", nil, append([]string{"commit", "--no-edit"}, q.Commit.Flags(false, prepared)...)...)
			} else {
				r, e = s.step("", nil, "merge", "--continue")
			}
		default:
			r, e = s.step("", nil, "rebase", "--continue")
		}
		if e != nil {
			return e
		}
	}
	active, e := g.Active(s.ctx)
	if e != nil {
		return e
	}
	stages, e := g.Conflicts(s.ctx)
	if e != nil {
		return e
	}
	if active || len(stages) > 0 {
		return stopped("Git integration did not complete")
	}
	return s.completed()
}
func appendRecord(value any, r any) []any { a, _ := value.([]any); return append(a, r) }
func (s *session) initialRebase() (process.Result, error) {
	if e := s.selection(); e != nil {
		return process.Result{}, e
	}
	q := s.request
	p := s.plan
	args := []string{"rebase", "--merge", "--no-autostash", "--no-update-refs", "--no-rebase-merges", "--onto", p.BaseOID, "--no-fork-point"}
	if q.Rebase.ReapplyCherryPicks == options.Unspecified && p.Reapply {
		args = append(args, "--reapply-cherry-picks")
	}
	if p.RequiresSequence && q.Rebase.Autosquash != options.Enabled {
		args = append(args, "--interactive", "--no-autosquash")
		if q.Rebase.Empty == "" {
			args = append(args, "--empty=drop")
		}
	}
	args = append(args, q.Rebase.Flags()...)
	args = append(args, q.Commit.Flags(false, false)...)
	args = append(args, p.SelectionOID)
	if q.Rebase.Autosquash != options.Enabled && !p.RequiresSequence {
		return s.step("", nil, args...)
	}
	executable, e := os.Executable()
	if e != nil {
		return process.Result{}, e
	}
	channel, e := os.CreateTemp("", "git-ez-sequence-")
	if e != nil {
		return process.Result{}, e
	}
	defer channel.Close()
	if e = os.Remove(channel.Name()); e != nil {
		return process.Result{}, e
	}
	capture := sequence.Capture{Directory: s.journal.Directory, Limit: int64(s.engine.Config.MaxFileBytes), Secrets: []string{s.engine.Config.ResolvedAPIKey(s.lookupEnv)}}
	if e = json.NewEncoder(channel).Encode(capture); e != nil {
		return process.Result{}, e
	}
	if _, e = channel.Seek(0, 0); e != nil {
		return process.Result{}, e
	}
	s.journal.View["rebase_sequence"] = map[string]any{"status": "pending", "artifact": "rebase-todo.txt", "limit_bytes": capture.Limit}
	if e = s.journal.Save(); e != nil {
		return process.Result{}, e
	}
	r, stepErr := s.step(sequence.Command(executable), []*os.File{channel}, append([]string{"-c", "rebase.instructionFormat=%s"}, args...)...)
	if _, e = channel.Seek(0, 0); e != nil {
		return r, e
	}
	if e = json.NewDecoder(channel).Decode(&capture); e != nil {
		return r, stopped("Native sequence helper did not report capture")
	}
	capture.Secrets = nil
	s.capture = &capture
	s.journal.View["rebase_sequence"] = map[string]any{"status": capture.Phase, "artifact": "rebase-todo.txt", "digest_artifact": "rebase-todo.txt.sha256", "metadata_artifact": "rebase-todo.txt.capture.json", "source_bytes": capture.SourceBytes, "source_sha256": capture.SourceHash, "sha256": capture.Hash, "redacted": capture.Redacted}
	if e = s.journal.JSON("rebase-todo.txt.capture.json", map[string]any{"source_bytes": capture.SourceBytes, "source_sha256": capture.SourceHash, "retained_sha256": capture.Hash, "redacted": capture.Redacted}); e != nil {
		return r, e
	}
	if e = s.journal.Save(); e != nil {
		return r, e
	}
	if e = sequence.Verify(capture); e != nil {
		return r, e
	}
	return r, stepErr
}
func (s *session) rebaseAction() (string, error) {
	b, e := s.engine.Git.ReadMetadata(s.ctx, "rebase-merge/done", int64(s.engine.Config.MaxFileBytes))
	if e != nil {
		return "", e
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch fields[0] {
		case "pick", "p":
			return "pick", nil
		case "fixup", "f":
			return "fixup", nil
		case "squash", "s":
			return "squash", nil
		}
		break
	}
	return "", stopped("Cannot determine current autosquash action")
}

func (p *Plan) MarshalJSON() ([]byte, error) {
	fork := map[string]any{"enabled": p.ForkEnabled, "oid": nullable(p.ForkOID), "source_ref": nullable(p.ForkRef), "status": p.ForkStatus}
	if p.ForkDiagnostic != "" {
		fork["diagnostic"] = p.ForkDiagnostic
	}
	if p.ForkStatus == "fallback" {
		fork["reason"] = "No usable reflog fork point; retaining the pinned replay boundary."
	}
	return json.Marshal(map[string]any{"base_oid": p.BaseOID, "base_source": p.BaseSource, "upstream_oid": p.UpstreamOID, "selection_oid": p.SelectionOID, "requires_sequence": p.RequiresSequence, "reapply_cherry_picks": p.Reapply, "fork_point": fork})
}
