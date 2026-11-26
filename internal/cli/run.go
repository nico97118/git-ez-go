// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/nico97118/git-ez-go/internal/failure"
)

type Streams struct {
	In       io.Reader
	Out, Err io.Writer
}

// Handler is the application boundary. It returns native Git status unchanged
// where applicable. Domain errors use failure.ExitCode. No AI framework appears
// in this contract; the Phase 3 engine will implement integration dispatch.
type Handler interface {
	Execute(context.Context, Request, Streams) (int, error)
}

func RunCLI(ctx context.Context, args []string, version string, streams Streams, handler Handler) int {
	r, e := Parse(args)
	if e != nil {
		return diagnostic(streams.Err, e)
	}
	if r.Action == Version {
		if _, e = fmt.Fprintln(streams.Out, "git-ez "+version); e != nil {
			return 2
		}
		return 0
	}
	if r.Action == Help {
		if _, e = io.WriteString(streams.Out, HelpText(r)); e != nil {
			return 2
		}
		return 0
	}
	r, e = r.Prepare()
	if e != nil {
		return diagnostic(streams.Err, e)
	}
	if e = ctx.Err(); e != nil {
		return diagnostic(streams.Err, e)
	}
	if handler == nil {
		return diagnostic(streams.Err, failure.New(failure.Precondition, "This foundation build has no integration, probe or audit service yet; see the Phase 2 milestone report"))
	}
	code, e := handler.Execute(ctx, r, streams)
	if e != nil {
		code = diagnostic(streams.Err, e)
		// Python reports an interrupted assisted noninteractive integration as
		// a failed run; interactive and foreground native interruption use 130.
		integration := r.Command == "merge" || r.Command == "rebase" || r.Command == "cherry-pick"
		if integration && !r.Continue && !r.Interactive && errors.Is(e, context.Canceled) {
			return 1
		}
		return code
	}
	return code
}
func diagnostic(w io.Writer, e error) int {
	_, _ = fmt.Fprintln(w, "git-ez: "+safeText(e.Error()))
	return failure.ExitCode(e)
}
func safeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) || r == '`' {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func HelpText(r Request) string {
	var b strings.Builder
	path := "git ez"
	if r.Command != "" {
		path += " " + r.Command
	}
	if r.AuditCommand != "" {
		path += " " + r.AuditCommand
	}
	fmt.Fprintf(&b, "usage: %s", path)
	switch r.Command {
	case "":
		b.WriteString(" {rebase,merge,cherry-pick,probe,audit} ...\n\nLocal Git integration with traceable AI conflict resolution.\n")
	case "audit":
		if r.AuditCommand == "" {
			b.WriteString(" {list,show} ...\n")
		} else if r.AuditCommand == "show" {
			b.WriteString(" [run-id] [--path directory] [--verbose]\n")
		} else {
			b.WriteByte('\n')
		}
	case "probe":
		b.WriteString("\n\nWrite the final JSON compatibility report to stdout; diagnostics use stderr.\n")
	default:
		b.WriteString(" [options] <target> | --continue\n")
	}
	b.WriteString("\noptions:\n")
	for _, flag := range specs(r.Command, r.AuditCommand) {
		fmt.Fprintf(&b, "  --%s", flag.name)
		if flag.short != "" {
			fmt.Fprintf(&b, ", -%s", flag.short)
		}
		if flag.arity == 1 {
			b.WriteString(" VALUE")
		} else if flag.arity == 2 {
			b.WriteString("[=KEY]")
		}
		b.WriteByte('\n')
	}
	if r.Command == "rebase" || r.Command == "merge" || r.Command == "cherry-pick" {
		b.WriteString("\n--check must be final; following arguments are literal executable arguments.\n")
	}
	return b.String()
}
