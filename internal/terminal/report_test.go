// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package terminal

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPipedLevelsAndRedaction(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, level := range []Level{Quiet, Default, Verbose} {
		var out bytes.Buffer
		r := New(&out, level, "secret")
		done := r.Begin(context.Background(), "Waiting secret\x1b")
		r.Info("progress")
		r.Detail("tool metadata")
		done()
		done()
		r.Stop()
		r.Diagnostic("warning secret\x1b")
		text := out.String()
		if strings.ContainsAny(text, "\x1b\r") || strings.Contains(text, "secret") || !strings.Contains(text, "warning <redacted>") {
			t.Fatal(text)
		}
		if strings.Contains(text, "progress") != (level != Quiet) || strings.Contains(text, "tool metadata") != (level == Verbose) {
			t.Fatal(level, text)
		}
	}
}
func TestAnimationJoinsOnCancellationAndStop(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		var out bytes.Buffer
		r := New(&out, Default, "")
		r.tty = true
		ctx, cancel := context.WithCancel(context.Background())
		done := r.Begin(ctx, "Waiting")
		if cancelFirst {
			cancel()
		}
		done()
		r.Stop()
		cancel()
		r.Diagnostic("Finished")
		// Read only after joining. No exact frame or wall-clock assertion.
		if !strings.HasSuffix(out.String(), "Finished\n") || r.active != nil {
			t.Fatal(out.String())
		}
	}
}
func TestCommandKeepsArgumentBoundaries(t *testing.T) {
	if got := Command([]string{"make", "a b", "x'y", ""}); got != "make 'a b' 'x'\"'\"'y' ''" {
		t.Fatal(got)
	}
}
