// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import (
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/failure"
	"strconv"
	"strings"
	"unicode/utf8"
)

type position struct {
	Backend                   string
	Step                      int
	OID, Branch, Before, Onto string
}

func (s *session) metadata(name string) (string, error) {
	b, e := s.engine.Git.ReadMetadata(s.ctx, name, 4096)
	if e != nil {
		return "", e
	}
	if !utf8.Valid(b) {
		return "", stopped("Non-UTF-8 Git metadata")
	}
	return strings.TrimSpace(string(b)), nil
}
func (s *session) position() *position {
	for _, backend := range []string{"rebase-merge", "rebase-apply"} {
		active, e := s.engine.Git.HasMarker(s.ctx, backend)
		if e != nil || !active {
			continue
		}
		p := &position{Backend: backend}
		counter, commit := "msgnum", "stopped-sha"
		if backend == "rebase-apply" {
			counter, commit = "next", "original-commit"
		}
		value, e := s.metadata(backend + "/" + counter)
		if e != nil {
			return nil
		}
		p.Step, e = strconv.Atoi(value)
		if e != nil || p.Step < 1 {
			return nil
		}
		oid, e := s.metadata(backend + "/" + commit)
		if e != nil {
			return nil
		}
		p.OID, e = s.engine.Git.CommitOID(s.ctx, oid)
		if e != nil {
			return nil
		}
		head, e := s.engine.Git.CommitOID(s.ctx, "REBASE_HEAD")
		if e != nil || head != p.OID {
			return nil
		}
		if backend == "rebase-merge" {
			done, e := s.engine.Git.ReadMetadata(s.ctx, backend+"/done", 1_000_000)
			if e != nil {
				return nil
			}
			lines := strings.Split(strings.TrimSpace(string(done)), "\n")
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) < 2 {
				return nil
			}
			switch fields[0] {
			case "pick", "reword", "squash", "fixup":
			default:
				return nil
			}
			i := 1
			if fields[0] == "fixup" && (fields[1] == "-C" || fields[1] == "-c") {
				i = 2
			}
			if len(fields) <= i || !strings.HasPrefix(p.OID, fields[i]) {
				return nil
			}
		}
		if p.Branch, e = s.metadata(backend + "/head-name"); e != nil {
			return nil
		}
		if p.Before, e = s.metadata(backend + "/orig-head"); e != nil {
			return nil
		}
		if p.Onto, e = s.metadata(backend + "/onto"); e != nil {
			return nil
		}
		return p
	}
	return nil
}
func (s *session) continueNative() (Result, error) {
	q := s.request
	g := s.engine.Git
	active, e := g.IntegrationActive(s.ctx, q.Operation)
	if e != nil {
		return s.result, e
	}
	if !active {
		return s.result, bad("No " + q.Operation + " in progress")
	}
	if q.Operation == "cherry-pick" {
		seq, e := g.HasMarker(s.ctx, "sequencer")
		if e != nil {
			return s.result, e
		}
		if seq {
			return s.result, bad("Cherry-pick sequences require native Git continuation")
		}
	}
	previous := s.position()
	branch, before, base := "", "", ""
	if previous != nil {
		branch, before, base = previous.Branch, previous.Before, previous.Onto
	} else if q.Operation != "rebase" && len(q.Validation) > 0 {
		branch, _ = s.text("symbolic-ref", "HEAD")
		before, _ = g.CommitOID(s.ctx, "HEAD")
		marker := "MERGE_HEAD"
		if q.Operation == "cherry-pick" {
			marker = "CHERRY_PICK_HEAD"
		}
		base, _ = g.CommitOID(s.ctx, marker)
	}
	r, e := g.Foreground(s.ctx, s.engine.In, s.engine.Out, s.engine.Err, q.Operation, "--continue")
	if e != nil {
		return s.result, e
	}
	code := r.ExitCode
	s.result.NativeStatus = &code
	if code == 0 {
		active, checkErr := g.Active(s.ctx)
		if checkErr != nil {
			return s.result, checkErr
		}
		if !active {
			s.result.Integrated = true
			s.result.ResultOID, _ = g.CommitOID(s.ctx, "HEAD")
			s.result.Status = "native_completed"
		}
	}
	if code == 0 && len(q.Validation) > 0 {
		active, e = g.Active(s.ctx)
		if e != nil {
			return s.result, e
		}
		if active {
			return s.result, stopped("Git stopped before completion; final validation was not run")
		}
		current, e := s.text("symbolic-ref", "HEAD")
		if e != nil {
			return s.result, e
		}
		if branch == "" || before == "" || base == "" || current != branch {
			return s.result, stopped("Git completed; original operation metadata unavailable or changed before validation")
		}
		s.branch = strings.TrimPrefix(branch, "refs/heads/")
		s.before = before
		s.target.OID = base
		s.request.Target = base
		if s.engine.Config.Model == "" {
			c, configErr := config.Load(s.ctx, g)
			if configErr != nil {
				return s.result, failure.Wrap(failure.Configuration, "Git completed; final validation configuration unavailable", configErr)
			}
			s.engine.Config = c
		}
		if e = s.startJournal(); e != nil {
			return s.result, e
		}
		s.journal.View["continuation"] = true
		if e = s.completed(); e != nil {
			_ = s.journal.Close()
			return s.result, e
		}
		return s.execute(false)
	}
	if code == 0 || q.Operation != "rebase" {
		return s.result, nil
	}
	following := s.position()
	if previous == nil || following == nil || previous.Backend != following.Backend || following.Step <= previous.Step || following.OID == previous.OID || previous.Branch != following.Branch || previous.Before != following.Before || previous.Onto != following.Onto || !strings.HasPrefix(previous.Branch, "refs/heads/") {
		return s.result, nil
	}
	conflicts, e := g.Conflicts(s.ctx)
	if e != nil {
		return s.result, e
	}
	if len(conflicts) == 0 {
		return s.result, nil
	}
	s.branch = strings.TrimPrefix(previous.Branch, "refs/heads/")
	oid, e := g.BranchOID(s.ctx, s.branch)
	if e != nil || oid != previous.Before {
		return s.result, nil
	}
	s.before = previous.Before
	s.target.OID = previous.Onto
	s.request.Target = previous.Onto
	if s.engine.Config.Model == "" {
		c, configErr := config.Load(s.ctx, g)
		if configErr != nil {
			return s.result, configErr
		}
		s.engine.Config = c
	}
	if e = s.engine.Config.Validate(); e != nil {
		return s.result, e
	}
	if e = s.startJournal(); e != nil {
		return s.result, e
	}
	s.journal.View["continuation"] = true
	s.result.NativeStatus = nil
	return s.execute(true)
}
