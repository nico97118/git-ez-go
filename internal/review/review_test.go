// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package review

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestExplicitChoicesAndFailedCheckCannotBeAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, input, action, feedback string
		failed                        bool
	}{
		{"accept", "accept\n", "accept", "", false}, {"quit", "s\n", "stop", "", false}, {"invalid-feedback", "\ninvalid\nr\n\nr\nkeep both\n", "revise", "keep both", false}, {"failed-accept", "a\nq\n", "stop", "", true}, {"failed-retry", "retry\nfix tests\n", "revise", "fix tests", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			a, f, e := Prompt(context.Background(), NewInput(strings.NewReader(tc.input)), &out, Proposal{Display: "file\x1b[2J\rforged", Diagnostic: "failed check", Confidence: .9, ValidationFailed: tc.failed})
			if e != nil || a != tc.action || f != tc.feedback {
				t.Fatal(a, f, e)
			}
			if strings.ContainsAny(out.String(), "\x1b\r") {
				t.Fatal("terminal controls displayed")
			}
			if tc.failed && strings.Contains(out.String(), "Accept /") {
				t.Fatal("failed check offered acceptance")
			}
		})
	}
}
func TestEOFAndCancellation(t *testing.T) {
	if _, _, e := Prompt(context.Background(), NewInput(strings.NewReader("")), io.Discard, Proposal{}); e == nil {
		t.Fatal("EOF accepted")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, e := Prompt(ctx, NewInput(reader), io.Discard, Proposal{}); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
}

func TestStreamingDisplayEscapesControlsAcrossUTF8Boundaries(t *testing.T) {
	source := strings.Repeat("é日\x1b\r\u202e\n", 10000)
	var out bytes.Buffer
	if err := WriteSafe(&out, strings.NewReader(source)); err != nil {
		t.Fatal(err)
	}
	if out.String() != SafeText(source) {
		t.Fatal("streaming display differs from safe text")
	}
}
