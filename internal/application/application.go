// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package application connects the CLI to the native engine, provider adapter
// and offline audit consultation.
package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/nico97118/git-ez-go/internal/agent"
	"github.com/nico97118/git-ez-go/internal/audit"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/resolver"
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
	selected := a.Resolver
	if selected == nil {
		selected = agent.New(c)
	}
	en, e := engine.New(ctx, g, c, selected)
	if e != nil {
		return 2, e
	}
	en.In = streams.In
	en.Out = streams.Out
	en.Err = streams.Err
	target := ""
	if q.Target != nil {
		target = *q.Target
	}
	r, e := en.Run(ctx, engine.Request{Operation: q.Command, Target: target, Onto: q.Onto, Rebase: q.Rebase, Commit: q.Commit, MergeFF: q.MergeFF, Rerere: q.RerereAutoupdate, Validation: q.Check, Continue: q.Continue, Interactive: q.Interactive})
	if (errors.Is(e, context.Canceled) || r.Status == "interrupted") && !failure.IsKind(e, failure.Cleanup) {
		continuationHint(ctx, en.Git, en.Config, q, streams.Err)
	}
	if r.Status != "" {
		// Result metadata is independent from command success; a failed final check
		// must never recommend continuing a native operation that already completed.
		text := fmt.Sprintf("Status: %s\nGit integration completed: %t\n", r.Status, r.Integrated)
		if r.Directory != "" {
			text += "Journal: " + r.Directory + "\n"
		}
		if token := en.Config.ResolvedAPIKey(nil); token != "" {
			text = strings.ReplaceAll(text, token, "<redacted>")
		}
		var safe strings.Builder
		for _, ch := range text {
			if ch == '\n' || unicode.IsPrint(ch) && ch != '`' {
				safe.WriteRune(ch)
			} else {
				fmt.Fprintf(&safe, "\\u%04x", ch)
			}
		}
		if _, writeErr := fmt.Fprint(streams.Out, safe.String()); writeErr != nil && e == nil {
			return 1, failure.Wrap(failure.Execution, "Cannot write integration result", writeErr)
		}
	}
	if e != nil {
		return failure.ExitCode(e), e
	}
	if r.NativeStatus != nil {
		return *r.NativeStatus, nil
	}
	return 0, nil
}
