// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine

import (
	"fmt"
	"io"
	"sync"

	"github.com/nico97118/git-ez-go/internal/journal"
)

// A diagnostic storage error must not become a process-copy error or change the
// check's exit status. The bounded preview is filtered before it is clipped.
type optionalDiagnostic struct {
	mu      sync.Mutex
	closed  bool
	capture *journal.Capture
	preview validationCapture
	filter  io.WriteCloser
	err     error
}

func newDiagnostic(j *journal.Journal, name string) *optionalDiagnostic {
	d := &optionalDiagnostic{preview: validationCapture{limit: 8192}}
	d.capture, d.err = j.Capture(name)
	d.filter = j.FilterWriter(&d.preview)
	return d
}
func (d *optionalDiagnostic) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return len(p), nil
	}
	_, _ = d.filter.Write(p)
	if d.capture != nil && d.err == nil {
		_, d.err = d.capture.Write(p)
	}
	return len(p), nil
}
func (d *optionalDiagnostic) finish(started bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	_ = d.filter.Close()
	if d.capture != nil {
		if started {
			if err := d.capture.Close(); d.err == nil {
				d.err = err
			}
		} else {
			_ = d.capture.Abort()
		}
	}
	return started && d.err == nil
}
func (s *session) diagnosticWarning(v map[string]any, stream string) {
	warning := fmt.Sprintf("Validation %s evidence is incomplete; validation status is unchanged. Rerun the validation command to recover missing diagnostics.", stream)
	v["diagnostics_incomplete"] = true
	warnings, _ := v["diagnostic_warnings"].([]string)
	v["diagnostic_warnings"] = append(warnings, warning)
	if s.engine.Err != nil {
		fmt.Fprintln(s.engine.Err, "git-ez: warning:", warning)
	}
}
