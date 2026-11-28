// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package review presents one global captured proposal. It never stages files.
package review

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type Input struct{ reader *bufio.Reader }

func NewInput(r io.Reader) *Input {
	if r == nil {
		r = strings.NewReader("")
	}
	return &Input{reader: bufio.NewReader(r)}
}

// Line has at most one pending read. Generic caller-owned readers cannot be
// forcibly closed; cancellation releases the engine without owning that reader.
func (i *Input) Line(ctx context.Context) (string, error) {
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, e := i.reader.ReadString('\n')
		if e == io.EOF && text != "" {
			e = nil
		}
		done <- result{text, e}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return strings.TrimSpace(r.text), r.err
	}
}

// SafeText retains line breaks while escaping terminal controls and format marks.
func SafeText(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

type Proposal struct {
	Render              func(io.Writer) error
	Display, Diagnostic string
	Validation          string
	Confidence          float64
	ValidationFailed    bool
}

func Prompt(ctx context.Context, in *Input, out io.Writer, p Proposal) (action, feedback string, err error) {
	if out == nil {
		out = io.Discard
	}
	display := func(text string) error { _, e := fmt.Fprintln(out, SafeText(text)); return e }
	if p.Render != nil {
		err = p.Render(out)
	} else {
		err = display(p.Display)
	}
	if err != nil {
		return
	}
	if err = display("Validation (git-ez):\n  Proposal content and conflict-marker checks passed.\n  " + p.Validation); err != nil {
		return
	}
	if p.ValidationFailed {
		if err = display("Check failed; this proposal cannot be accepted.\n" + p.Diagnostic); err != nil {
			return
		}
	} else {
		if err = display("Proposal ready for review; final integration verification remains required."); err != nil {
			return
		}
	}
	for {
		prompt := "[a] Accept / [r] Revise (reject and reprompt) / [q] Quit\nChoice: "
		if p.ValidationFailed {
			prompt = "[r] Retry with feedback / [q] Quit: "
		}
		if _, err = io.WriteString(out, prompt); err != nil {
			return
		}
		var answer string
		answer, err = in.Line(ctx)
		if err != nil {
			return
		}
		switch strings.ToLower(answer) {
		case "a", "accept":
			if !p.ValidationFailed {
				return "accept", "", nil
			}
		case "q", "quit", "s", "stop":
			return "stop", "", nil
		case "r", "revise", "retry", "reject":
			if _, err = io.WriteString(out, "Revision feedback: "); err != nil {
				return
			}
			feedback, err = in.Line(ctx)
			if err != nil {
				return
			}
			if feedback != "" {
				return "revise", feedback, nil
			}
		}
		if err = display("An explicit choice and nonempty revision feedback are required."); err != nil {
			return
		}
	}
}

// WriteSafe streams UTF-8 terminal text without retaining whole artifacts.
func WriteSafe(out io.Writer, in io.Reader) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	for {
		r, _, e := reader.ReadRune()
		if e == io.EOF {
			return writer.Flush()
		}
		if e != nil {
			return e
		}
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			if _, e = fmt.Fprintf(writer, "\\u%04x", r); e != nil {
				return e
			}
		} else {
			if _, e = writer.WriteRune(r); e != nil {
				return e
			}
		}
	}
}
