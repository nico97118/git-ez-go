// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"

	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/sequence"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

type step struct {
	thinking string
	name     string
	args     map[string]any
	text     string
	batch    []step
}

func read(path string) step { return step{name: "read_file", args: map[string]any{"filePath": path}} }
func write(path, text string) step {
	return step{name: "write_file", args: map[string]any{"filePath": path, "content": text}}
}
func finish(confidence float64) step {
	return step{name: "finish", args: map[string]any{"rationale": "Preserved both branches and checked the project", "confidence": confidence}}
}
func command(text string) step { return step{name: "execute", args: map[string]any{"command": text}} }

type modelFixture struct {
	tokenInput, tokenOutput  int
	mu                       sync.Mutex
	steps                    []step
	requests                 []map[string]any
	auth, paths              []string
	summaries, index, status int
	delay                    time.Duration
	secret                   string
}

func (f *modelFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var input map[string]any
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "bad JSON", 400)
		return
	}
	if f.delay > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(f.delay):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, input)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.paths = append(f.paths, r.URL.Path)
	if f.status != 0 {
		http.Error(w, "provider echoes "+f.secret, f.status)
		return
	}
	raw, _ := json.Marshal(input)
	next := step{text: "Stopped without finish"}
	if strings.Contains(string(raw), "Summarize the following conversation concisely") {
		f.summaries++
		next.text = "Historical evidence only. Both branch intents must be preserved. Continue investigating and edit unresolved files; finish alone."
	} else if f.index < len(f.steps) {
		next = f.steps[f.index]
		f.index++
	}
	batch := next.batch
	if next.name != "" {
		batch = []step{next}
	}
	calls := []any{}
	inputTokens, outputTokens := 10, 1
	if f.tokenInput > 0 {
		inputTokens = f.tokenInput
	}
	if f.tokenOutput > 0 {
		outputTokens = f.tokenOutput
	}
	if strings.HasSuffix(r.URL.Path, "/api/chat") {
		for _, s := range batch {
			calls = append(calls, map[string]any{"function": map[string]any{"name": s.name, "arguments": s.args}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "done": true, "done_reason": "stop", "message": map[string]any{"role": "assistant", "content": next.text, "thinking": next.thinking, "tool_calls": calls}, "prompt_eval_count": inputTokens, "eval_count": outputTokens})
	} else {
		for _, s := range batch {
			args, _ := json.Marshal(s.args)
			calls = append(calls, map[string]any{"id": fmt.Sprintf("call-%d-%d", len(f.requests), len(calls)), "type": "function", "function": map[string]any{"name": s.name, "arguments": string(args)}})
		}
		reason := "stop"
		if len(calls) > 0 {
			reason = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "local-fixture", "object": "chat.completion", "model": "fixture", "choices": []any{map[string]any{"index": 0, "finish_reason": reason, "message": map[string]any{"role": "assistant", "content": next.text, "tool_calls": calls}}}, "usage": map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens, "total_tokens": inputTokens + outputTokens}})
	}
}
func setup(t *testing.T, provider string, steps ...step) (config.Config, git.Client, *modelFixture, func()) {
	t.Helper()
	fixture := &modelFixture{steps: steps}
	server := httptest.NewServer(fixture)
	c := config.Defaults()
	c.Model = "fixture"
	c.Provider = provider
	c.Endpoint = server.URL + "/v1/chat/completions"
	if provider == "ollama" {
		c.Endpoint = server.URL + "/api/chat"
	}
	g := testutil.Conflicted(t)
	g.Env = testutil.SetEnv(g.Env, "GIT_EDITOR", "true")
	return c, g, fixture, server.Close
}
func runEngine(t *testing.T, c config.Config, g git.Client) (engine.Result, error) {
	t.Helper()
	testutil.Must(t, g, "merge", "--abort")
	en, e := engine.New(context.Background(), g, c, New(c))
	if e != nil {
		t.Fatal(e)
	}
	result, err := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "feature"})
	if err != nil {
		files, _ := filepath.Glob(filepath.Join(result.Directory, "agent-*", "session.json"))
		for _, file := range files {
			raw, _ := os.ReadFile(file)
			t.Log(string(raw))
		}
	}
	return result, err
}
func TestProductionProvidersExecuteNativeToolsThroughEngine(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated || r.ResolverCalls != 1 {
				t.Fatal(r, e)
			}
			if testutil.Must(t, g, "rev-parse", "HEAD^1") != before || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("engine topology/state")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 3 {
				t.Fatalf("native calls %d", len(f.requests))
			}
			for _, p := range f.paths {
				if provider == "ollama" && p != "/api/chat" || provider == "openai" && p != "/v1/chat/completions" {
					t.Fatal(p)
				}
			}
			artifacts, e := filepath.Glob(filepath.Join(r.Directory, "agent-*", "session.json"))
			if e != nil || len(artifacts) != 1 {
				t.Fatal("session retention", artifacts, e)
			}
		})
	}
}
func TestFinishErrorsAndFeedback(t *testing.T) {
	cases := map[string][]step{
		"missing": {{text: "All done"}}, "low-confidence": {read("file.txt"), write("file.txt", "combined\n"), finish(.1)},
		"marker-repair":         {read("file.txt"), write("file.txt", "<<<<<<< remains\n"), finish(.95), write("file.txt", "combined\n"), finish(.95)},
		"batched-repair":        {read("file.txt"), {batch: []step{write("file.txt", "combined\n"), finish(.95)}}, finish(.95)},
		"metadata-repair":       {read(".git/config"), read("file.txt"), write("file.txt", "combined\n"), finish(.95)},
		"no-edit":               {finish(.95)},
		"invalid-finish-repair": {read("file.txt"), write("file.txt", "combined\n"), {name: "finish", args: map[string]any{"rationale": "why", "confidence": "high"}}, finish(.95)},
		"extra-finish-repair":   {read("file.txt"), write("file.txt", "combined\n"), {name: "finish", args: map[string]any{"rationale": "why", "confidence": .95, "extra": true}}, finish(.95)},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			c, g, _, close := setup(t, "openai", steps...)
			defer close()
			r, e := runEngine(t, c, g)
			good := strings.HasSuffix(name, "repair")
			if good && (e != nil || !r.Integrated) {
				t.Fatal(r, e)
			}
			if !good && (e == nil || r.Integrated) {
				t.Fatal(r, e)
			}

			events, _ := filepath.Glob(filepath.Join(r.Directory, "agent-*", "event-*.json"))
			started, results := 0, 0
			for _, path := range events {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var event map[string]any
				if err = json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
				if event["event"] == "tool_started" {
					started++
				}
				if event["event"] == "tool_result" {
					results++
				}
			}
			if started != results {
				t.Fatal("tool rejection/result evidence missing or duplicated", started, results)
			}
		})
	}
}
func TestHardBudgetsAndNoRetry(t *testing.T) {
	for _, kind := range []string{"models", "tools", "429", "302"} {
		t.Run(kind, func(t *testing.T) {
			c, g, f, close := setup(t, "openai", read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			switch kind {
			case "models":
				c.AgentMaxTurns = 1
			case "tools":
				c.AgentMaxToolCalls = 1
			case "429":
				f.status = 429
			case "302":
				f.status = 302
			}
			r, e := runEngine(t, c, g)
			if e == nil || r.Integrated {
				t.Fatal(r, e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if (kind == "models" || kind == "429" || kind == "302") && len(f.requests) != 1 {
				t.Fatal("budget/retry", len(f.requests))
			}
		})
	}
}
func TestForbiddenGitMutationAndManagedTimeout(t *testing.T) {
	for _, kind := range []string{"forbidden", "hidden-control", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			script := "git add file.txt"
			if kind == "hidden-control" {
				script = "printf changed > .git/MERGE_HEAD"
			}
			if kind == "timeout" {
				script = "printf partial; sleep 5"
			}
			steps := []step{command(script), read("file.txt"), write("file.txt", "combined\n"), finish(.95)}
			c, g, _, close := setup(t, "openai", steps...)
			defer close()
			c.Timeout = .2
			r, e := runEngine(t, c, g)
			if kind == "hidden-control" {
				if e == nil || r.Integrated {
					t.Fatal(r, e)
				}
			} else if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
		})
	}
}
func TestCompressionRetainsMissionAndCountsNestedRequests(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			steps := []step{read("file.txt")}
			for i := 0; i < 16; i++ {
				steps = append(steps, command("printf investigate"))
			}
			steps = append(steps, write("file.txt", "combined\n"), finish(.95))
			if provider == "ollama" {
				steps[0].thinking = "Investigate evidence before resolving."
			}
			c, g, f, close := setup(t, provider, steps...)
			defer close()
			c.AgentMaxTurns = 60
			f.tokenInput = c.ModelContextTokens - c.ModelMaxOutputTokens + 1
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			t.Logf("actual_model_calls=%d nested_summary_calls=%d scripted_tool_turns=%d", len(f.requests), f.summaries, len(steps))
			if f.summaries == 0 || len(f.requests) <= len(steps) {
				t.Fatal("nested summarization absent", f.summaries, len(f.requests))
			}
			for _, req := range f.requests {
				b, _ := json.Marshal(req["messages"])
				if !strings.Contains(string(b), "Authoritative engine mission") || !strings.Contains(string(b), "branch_sha") {
					t.Fatal("permanent mission lost")
				}
			}
			events, e := filepath.Glob(filepath.Join(r.Directory, "agent-*", "event-*.json"))
			if e != nil {
				t.Fatal(e)
			}
			compressed := false
			for _, name := range events {
				raw, e := os.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				var event map[string]any
				if e = json.Unmarshal(raw, &event); e != nil {
					t.Fatal(e)
				}
				if event["event"] == "context_compression" {
					compressed = true
				}
			}
			if !compressed {
				t.Fatal("native compression evidence was not retained")
			}

		})
	}
}

func TestAdapterCancellation(t *testing.T) {
	c, g, f, close := setup(t, "openai", read("file.txt"))
	defer close()
	f.delay = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	parent := filepath.Join(t.TempDir(), "run")
	if e := os.Mkdir(parent, 0700); e != nil {
		t.Fatal(e)
	}
	_, e := New(c).Resolve(ctx, resolver.Stop{Operation: "merge", Worktree: g.Dir, ArtifactDirectory: parent, Conflicts: []resolver.Conflict{{Path: "file.txt"}}})
	if e == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestMain(m *testing.M) {
	_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
	if len(os.Args) > 1 && os.Args[1] == sequence.Helper {
		os.Exit(sequence.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestCompressionUsesConfiguredContextAndOutputReservation(t *testing.T) {
	c := config.Defaults()
	c.ModelContextTokens = 64000
	c.ModelMaxOutputTokens = 4000
	s := session{config: c}
	compression := s.compression(ai.NewModelRef("fixture", nil))
	if compression.MaxInputTokens != 60000 || compression.MaxMessages != 0 || compression.TruncateToolResponses != nil || compression.MaxToolResponseChars != 0 {
		t.Fatalf("unexpected custom context policy: %+v", compression)
	}
}
