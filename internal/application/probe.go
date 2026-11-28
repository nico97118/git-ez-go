// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nico97118/git-ez-go/internal/review"
)

// probeText presents only recorded outcomes. Responses, tool payloads and
// implementation error types remain available in the explicitly requested JSON.
func probeText(raw []byte) (string, error) {
	var report struct {
		Provider, Model, Status string
		Checks                  []struct {
			Name string
			OK   bool
		}
		Error *struct {
			Message    string
			StatusCode *int `json:"status_code"`
		}
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Provider: %s\nModel: %s\n\n", review.SafeText(report.Provider), review.SafeText(report.Model))
	checks := map[string]bool{}
	for _, check := range report.Checks {
		checks[check.Name] = check.OK
	}
	for _, check := range []struct{ name, label string }{
		{"chat", "Chat response"}, {"tool_call", "Native tool calling"}, {"tool_result_roundtrip", "Response after tool execution"},
	} {
		status := "NOT RUN"
		if ok, present := checks[check.name]; present {
			status = "FAIL"
			if ok {
				status = "OK"
			}
		}
		fmt.Fprintf(&out, "[%s] %s\n", status, check.label)
	}
	if report.Status == "compatible" {
		out.WriteString("\nProvider protocol is compatible.\n")
	} else {
		out.WriteString("\nProvider protocol compatibility could not be confirmed.\n")
		if report.Error != nil {
			if report.Error.Message != "" {
				fmt.Fprintf(&out, "Reason: %s\n", review.SafeText(report.Error.Message))
			}
			if report.Error.StatusCode != nil {
				fmt.Fprintf(&out, "HTTP status: %d\n", *report.Error.StatusCode)
			}
		}
	}
	out.WriteString("Conflict-resolution quality was not tested.")
	return out.String(), nil
}
