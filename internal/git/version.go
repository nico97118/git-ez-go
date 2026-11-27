// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package git

import (
	"context"
	"strconv"
	"strings"

	"github.com/nico97118/git-ez-go/internal/failure"
)

// RequireSupported rejects unsupported Git before an integration can mutate
// state. Accepted upstream Git limitations retain their native semantics.
func (c Client) RequireSupported(ctx context.Context) error {
	text, err := c.Text(ctx, "--version")
	if err != nil {
		return failure.Wrap(failure.Precondition, "Cannot determine native Git version; Git 2.38 or newer is required", err)
	}
	fields := strings.Fields(text)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return failure.New(failure.Precondition, "Cannot determine native Git version; Git 2.38 or newer is required")
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return failure.New(failure.Precondition, "Cannot determine native Git version; Git 2.38 or newer is required")
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 0 || minor < 0 {
		return failure.New(failure.Precondition, "Cannot determine native Git version; Git 2.38 or newer is required")
	}
	if major < 2 || major == 2 && minor < 38 {
		return failure.New(failure.Precondition, "Git 2.38 or newer is required; use native Git directly to finish existing operations")
	}
	return nil
}
