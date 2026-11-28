// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import "github.com/nico97118/git-ez-go/internal/resolver"

func (s *session) resolveAttempt(stop resolver.Stop, attempt int) (resolver.Decision, error) {
	s.engine.Report.Info("git-ez: Resolution attempt %d", attempt)
	done := s.engine.Report.Begin(s.ctx, "Resolving conflicts (waiting for model or tools)")
	defer done()
	return s.engine.Resolver.Resolve(s.ctx, stop)
}
