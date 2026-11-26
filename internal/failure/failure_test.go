// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package failure

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSafeDiagnosticRetainsCauseAndClassification(t *testing.T) {
	cause := errors.New("secret provider response")
	e := Wrap(Configuration, "Invalid model configuration", cause)
	if !errors.Is(e, cause) || strings.Contains(e.Error(), "secret") {
		t.Fatal("cause lost or exposed")
	}
	for _, test := range []struct {
		e    error
		code int
	}{{nil, 0}, {e, 2}, {New(Execution, "failed"), 1}, {New(Cleanup, "unknown child"), 1}, {New(Audit, "required save failed"), 1}, {context.Canceled, 130}} {
		if got := ExitCode(test.e); got != test.code {
			t.Fatalf("code %d != %d", got, test.code)
		}
	}
}
