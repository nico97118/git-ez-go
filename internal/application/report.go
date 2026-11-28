// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/terminal"
)

func operationSummary(ctx context.Context, en *engine.Engine, q cli.Request, r engine.Result, err error, report *terminal.Reporter, diagnostics io.Writer) {
	name := strings.ToUpper(q.Command[:1]) + q.Command[1:]
	if err == nil {
		if r.ResolverCalls == 0 {
			return
		}
		report.Info("git-ez: %s completed successfully.", name)
		report.Info("  %s resolved (%s)", terminal.Count(r.ConflictSets, "conflict set"), terminal.Count(r.ConflictFiles, "file occurrence"))
		report.Info("  Resolution time: %.1fs", r.ResolutionTime.Seconds())
		if r.Directory != "" {
			report.Info("  Audit: %s", r.Directory)
		}
		return
	}
	if r.Integrated {
		report.Diagnostic("git-ez: %s completed successfully.", name)
		if r.FinalValidationFailed {
			report.Diagnostic("git-ez: Final validation failed.")
		}
		report.Diagnostic("  Git integration is complete. Review the errors before proceeding.")
	} else if r.ResolverCalls > 0 {
		if errors.Is(err, context.Canceled) {
			report.Diagnostic("git-ez: Conflict resolution interrupted.")
		} else {
			report.Diagnostic("git-ez: Unable to resolve conflicts.")
		}
	}
	if r.Directory != "" {
		report.Diagnostic("  Audit: %s", r.Directory)
	}
	if !r.Integrated && !failure.IsKind(err, failure.Cleanup) {
		continuationHint(ctx, en.Git, en.Config, q, diagnostics)
	}
}
