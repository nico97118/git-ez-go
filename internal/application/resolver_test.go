// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/nico97118/git-ez-go/internal/application"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"
)

func TestCLIResolvesUsingActualGenkitAdapter(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid request")
		}
		calls++
		name := "read_file"
		args := map[string]any{"filePath": "file.txt"}
		if calls == 2 {
			name = "write_file"
			args["content"] = "combined\n"
		}
		if calls == 3 {
			name = "finish"
			args = map[string]any{"rationale": "hidden-reasoning authorization-secret", "confidence": .95}
		}
		payload, _ := json.Marshal(args)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "model": "fixture", "choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "tool", "type": "function", "function": map[string]any{"name": name, "arguments": string(payload)}}}}}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 1}})
	}))
	defer server.Close()
	g := testutil.Conflicted(t)
	testutil.Must(t, g, "merge", "--abort")
	testutil.Must(t, g, "config", "ez.model", "fixture")
	testutil.Must(t, g, "config", "ez.endpoint", server.URL+"/v1/chat/completions")
	testutil.Must(t, g, "config", "ez.apiKey", "authorization-secret")
	var out, err bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"merge", "feature", "--verbose", "--repo", g.Dir}, "test", cli.Streams{Out: &out, Err: &err}, application.Application{})
	if code != 0 || calls != 3 || !bytes.Contains(err.Bytes(), []byte("Merge completed successfully.")) || bytes.Contains(out.Bytes(), []byte("git-ez:")) {
		t.Fatal(code, calls, out.String(), err.String())
	}
	for _, want := range []string{"Waiting for model response", "Tool 1: read_file", "Reported tokens:", "Model request finished"} {
		if !bytes.Contains(err.Bytes(), []byte(want)) {
			t.Fatal(want, err.String())
		}
	}
	for _, forbidden := range []string{"authorization-secret", "hidden-reasoning", "filePath", "combined"} {
		if bytes.Contains(err.Bytes(), []byte(forbidden)) {
			t.Fatal("verbose exposed payload", err.String())
		}
	}

}

func TestCLIProbeUsesNativeConfigurationAndJSONStdout(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	t.Setenv("OPENAI_API_KEY", "")
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.model", "fixture")
	t.Chdir(g.Dir)
	var calls atomic.Int32
	markerPattern := regexp.MustCompile(`ez-[a-f0-9]{32}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		raw, _ := json.Marshal(request)
		marker := markerPattern.FindString(string(raw))
		call := calls.Add(1)
		message := map[string]any{"role": "assistant", "content": marker}
		reason := "stop"
		if call%3 == 2 {
			args, _ := json.Marshal(map[string]any{"value": marker})
			message["tool_calls"] = []any{map[string]any{"id": "echo", "type": "function", "function": map[string]any{"name": "compatibility_echo", "arguments": string(args)}}}
			reason = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "probe", "model": "fixture", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "finish_reason": reason, "message": message}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 1}})
	}))
	defer server.Close()
	testutil.Must(t, g, "config", "ez.endpoint", server.URL+"/v1/chat/completions")
	var out, err bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"probe"}, "test", cli.Streams{Out: &out, Err: &err}, application.Application{})
	var report map[string]any
	if code != 0 || calls.Load() != 3 || json.Unmarshal(out.Bytes(), &report) != nil || report["status"] != "compatible" || err.Len() != 0 {
		t.Fatal(code, out.String(), err.String())
	}

	err.Reset()
	code = cli.RunCLI(context.Background(), []string{"probe"}, "test", cli.Streams{Out: failedProbeWriter{}, Err: &err}, application.Application{})
	if code != 2 || calls.Load() != 6 {
		t.Fatal("probe stdout failure status", code, calls.Load(), err.String())
	}
}

type failedProbeWriter struct{}

func (failedProbeWriter) Write([]byte) (int, error) { return 0, errors.New("fixture output failure") }

func TestAuditConsultationDoesNotLoadModelConfiguration(t *testing.T) {
	t.Setenv("GENKIT_ENV", "dev")
	t.Setenv("GENKIT_REFLECTION_SERVER", "true")
	t.Setenv("OPENAI_API_KEY", "")
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.provider", "unsupported")
	testutil.Must(t, g, "config", "ez.endpoint", "not a URL")
	var out, diagnostics bytes.Buffer
	t.Chdir(g.Dir)
	code := cli.RunCLI(context.Background(), []string{"audit", "list"}, "test", cli.Streams{Out: &out, Err: &diagnostics}, application.Application{})
	if code != 0 || !bytes.Contains(out.Bytes(), []byte("No audit runs")) {
		t.Fatal(code, out.String(), diagnostics.String())
	}
}
