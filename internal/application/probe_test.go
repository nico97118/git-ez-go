// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application

import (
	"strings"
	"testing"
)

func TestProbeTextUsesRecordedChecksOnly(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"provider":"ollama","model":"fixture","status":"compatible","checks":[{"name":"chat","ok":true},{"name":"tool_call","ok":true},{"name":"tool_result_roundtrip","ok":true}]}`, "[OK] Response after tool execution"},
		{`{"status":"failed","checks":[{"name":"chat","ok":true},{"name":"tool_call","ok":false,"response":"private payload"}]}`, "[FAIL] Native tool calling\n[NOT RUN] Response after tool execution"},
		{`{"status":"failed","checks":[],"error":{"message":"Provider protocol compatibility failed","status_code":401,"type":"private type"}}`, "HTTP status: 401"},
	} {
		text, err := probeText([]byte(tc.raw))
		if err != nil || !strings.Contains(text, tc.want) || strings.Contains(text, "private") || !strings.Contains(text, "quality was not tested") {
			t.Fatal(text, err)
		}
		if strings.Contains(tc.raw, `"status":"failed"`) && strings.Contains(text, "protocol is compatible") {
			t.Fatal(text)
		}
	}
	if _, err := probeText([]byte("invalid")); err == nil {
		t.Fatal("invalid report accepted")
	}
}
