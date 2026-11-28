// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package application connects the CLI to the native engine, provider adapter
// and offline audit consultation.
package application

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/nico97118/git-ez-go/internal/agent"
	"github.com/nico97118/git-ez-go/internal/audit"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/terminal"
)

type Application struct{ Resolver resolver.Resolver }

func (a Application) Execute(ctx context.Context, q cli.Request, streams cli.Streams) (int, error) {
	if q.Command == "audit" {
		g, err := git.New(q.Repo)
		if err != nil {
			return 2, err
		}
		report, err := os.CreateTemp("", "git-ez-audit-report-")
		if err != nil {
			return 1, err
		}
		defer os.Remove(report.Name())
		defer report.Close()
		err = audit.ConsultTo(ctx, g, q.AuditCommand, q.RunID, q.AuditPath, q.Verbose, streams.Err, report)
		if err != nil {
			return 2, failure.Wrap(failure.Precondition, "Cannot consult audit: "+err.Error(), err)
		}
		if _, err = report.Seek(0, io.SeekStart); err != nil {
			return 1, err
		}
		if err = audit.DisplayReader(ctx, g, report, streams.Out, streams.Err); err != nil {
			return failure.ExitCode(err), err
		}
		return 0, nil
	}
	g, e := git.New(q.Repo)
	if e != nil {
		return 2, failure.Wrap(failure.Precondition, "Cannot open repository directory", e)
	}
	c := config.Defaults()
	if !q.Continue {
		c, e = config.Load(ctx, g)
		if e != nil {
			return 2, e
		}
	}
	if q.Command == "probe" {
		body, ok := agent.Probe(ctx, c)
		if _, err := fmt.Fprintln(streams.Out, string(body)); err != nil {
			return 2, failure.Wrap(failure.Precondition, "Cannot write provider probe report", err)
		}
		if !ok {
			return 1, nil
		}
		return 0, nil
	}
	level := terminal.Default
	if q.Quiet {
		level = terminal.Quiet
	} else if q.Verbose {
		level = terminal.Verbose
	}
	report := terminal.New(streams.Err, level, c.ResolvedAPIKey(nil))
	defer report.Stop()
	selected := a.Resolver
	if selected == nil {
		adapter := agent.New(c)
		adapter.Report = report
		selected = adapter
	}
	en, e := engine.New(ctx, g, c, selected)
	if e != nil {
		return 2, e
	}
	en.Report = report
	en.In = streams.In
	en.Out = streams.Out
	en.Err = streams.Err
	target := ""
	if q.Target != nil {
		target = *q.Target
	}
	r, e := en.Run(ctx, engine.Request{Operation: q.Command, Target: target, Onto: q.Onto, Rebase: q.Rebase, Commit: q.Commit, MergeFF: q.MergeFF, Rerere: q.RerereAutoupdate, Validation: q.Check, Continue: q.Continue, Interactive: q.Interactive})
	report.Stop()
	report.SetSecret(en.Config.ResolvedAPIKey(nil))
	operationSummary(ctx, en, q, r, e, report, streams.Err)

	if e != nil {
		return failure.ExitCode(e), e
	}
	if r.NativeStatus != nil {
		return *r.NativeStatus, nil
	}
	return 0, nil
}
