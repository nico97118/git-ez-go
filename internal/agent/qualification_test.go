// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/config"
)

func TestProbeRejectsProtocolFailuresWithoutFallback(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, kind := range []string{"chat", "missing-tool", "tool-name", "tool-arguments", "tool-id", "tool-result", "final-tool", "responses-endpoint"} {
			if provider == "ollama" && kind == "tool-id" {
				continue
			} // Native Ollama has no wire tool-call IDs.
			t.Run(provider+"/"+kind, func(t *testing.T) {
				var count atomic.Int32
				markerPattern := regexp.MustCompile(`ez-[a-f0-9]{32}`)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					marker := markerPattern.FindString(string(raw))
					number := int(count.Add(1))
					response := step{text: marker}
					if number == 2 && kind != "missing-tool" {
						response = step{name: "compatibility_echo", args: map[string]any{"value": marker}}
					}
					if (number == 1 && kind == "chat") || (number == 3 && kind == "tool-result") {
						response = step{text: "unrelated response"}
					}
					if number == 2 && kind == "tool-name" {
						response.name = "wrong_tool"
					}
					if number == 2 && kind == "tool-arguments" {
						response.args = map[string]any{"value": "wrong marker"}
					}
					if number == 3 && kind == "final-tool" {
						response = step{name: "compatibility_echo", args: map[string]any{"value": marker}}
					}
					local := &modelFixture{steps: []step{response}}
					recorder := httptest.NewRecorder()
					r.Body = io.NopCloser(strings.NewReader(string(raw)))
					local.ServeHTTP(recorder, r)
					var body map[string]any
					if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
						t.Error(err)
						return
					}
					if number == 2 && kind == "tool-id" {
						message := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
						message["tool_calls"].([]any)[0].(map[string]any)["id"] = ""
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(body)
				}))
				defer server.Close()
				c := config.Defaults()
				c.Provider = provider
				c.Model = "fixture"
				c.Endpoint = server.URL + "/v1/chat/completions"
				if provider == "ollama" {
					c.Endpoint = server.URL + "/api/chat"
				}
				if kind == "responses-endpoint" {
					c.Endpoint = server.URL + "/v1/responses"
				}
				report, ok := Probe(context.Background(), c)
				expected := 2
				if kind == "chat" {
					expected = 1
				}
				if kind == "tool-result" || kind == "final-tool" {
					expected = 3
				}
				if kind == "responses-endpoint" {
					expected = 0
				}
				if ok || int(count.Load()) != expected || !strings.Contains(string(report), `"status":"failed"`) {
					t.Fatal(ok, count.Load(), string(report))
				}
			})
		}
	}
}

func TestCommandEvidenceKeepsExitStreamsAndTiming(t *testing.T) {
	for _, kind := range []string{"nonzero", "timeout-text", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			script := "printf output; printf diagnostic >&2; exit 7"
			expected := 7
			if kind == "timeout-text" {
				script = "printf 'Command timed out'; exit 0"
				expected = 0
			}
			if kind == "timeout" {
				script = "printf before-timeout; sleep 20"
				expected = 124
			}
			call := command(script)
			if kind == "timeout" {
				call.args["timeout"] = .05
			}
			c, g, _, close := setup(t, "openai", call, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			r, err := runEngine(t, c, g)
			if err != nil || !r.Integrated {
				t.Fatal(r, err)
			}
			paths, _ := filepath.Glob(filepath.Join(r.Directory, "agent-*", "event-*.json"))
			starts, finishes, modelDurations := 0, 0, 0
			id := ""
			for _, path := range paths {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var event map[string]any
				if err = json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
				data, _ := event["data"].(map[string]any)
				if event["event"] == "command_started" {
					starts++
					id, _ = data["command_id"].(string)
				}
				if event["event"] == "command_finished" {
					finishes++
					duration, ok := data["duration_seconds"].(float64)
					if !ok || duration < 0 || data["command_id"] != id || id == "" {
						t.Fatal("command timing/link missing", data)
					}
					result := data["result"].(map[string]any)
					if result["exit_code"] != float64(expected) || result["TimedOut"] != (kind == "timeout") {
						t.Fatal(result)
					}
					stdout, err := os.ReadFile(filepath.Join(filepath.Dir(path), result["StdoutArtifact"].(string)))
					if err != nil || len(stdout) == 0 {
						t.Fatal("stdout missing", err)
					}
					if kind == "nonzero" {
						stderr, err := os.ReadFile(filepath.Join(filepath.Dir(path), result["StderrArtifact"].(string)))
						if err != nil || string(stderr) != "diagnostic" {
							t.Fatal(string(stderr), err)
						}
					}
				}
				if event["event"] == "model_http_response" {
					duration, ok := data["duration_seconds"].(float64)
					if !ok || duration < 0 {
						t.Fatal(data)
					}
					modelDurations++
				}
			}
			if starts != 1 || finishes != 1 || modelDurations != 4 {
				t.Fatal(starts, finishes, modelDurations)
			}
		})
	}
}

func TestCancellationWhileReadingProviderBodyRemainsAnInterruption(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		t.Run(fmt.Sprint(interactive), func(t *testing.T) {
			c, g, _, closeFixture := setup(t, "openai")
			closeFixture()
			testutil.Must(t, g, "merge", "--abort")
			ready := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				io.WriteString(w, `{"choices":[`)
				w.(http.Flusher).Flush()
				close(ready)
				<-r.Context().Done()
			}))
			defer server.Close()
			c.Endpoint = server.URL + "/v1/chat/completions"
			en, err := engine.New(context.Background(), g, c, New(c))
			if err != nil {
				t.Fatal(err)
			}
			en.Out = io.Discard
			en.In = strings.NewReader("a\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				select {
				case <-ready:
					cancel()
				case <-ctx.Done():
				}
			}()
			result, err := en.Run(ctx, engine.Request{Operation: "merge", Target: "feature", Interactive: interactive})
			if !errors.Is(err, context.Canceled) || result.Status != "interrupted" || result.Integrated {
				t.Fatal("lost provider-body cancellation", result, err)
			}
		})
	}
}

func TestProductionShellCancellationWaitsBeforeConflictRestoration(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", provider, interactive), func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "child-ready")
				script := fmt.Sprintf("printf 'unapproved\\n' > file.txt; printf 'unapproved\\n' > support.txt; printf ready > '%s'; sleep 1; printf late > file.txt; printf late > late-file", marker)
				c, g, _, closeFixture := setup(t, provider, command(script))
				defer closeFixture()
				testutil.Must(t, g, "merge", "--abort")
				testutil.Commit(t, g, "support.txt", "clean\n", "supporting file")
				en, err := engine.New(context.Background(), g, c, New(c))
				if err != nil {
					t.Fatal(err)
				}
				en.Out = io.Discard
				en.In = strings.NewReader("a\n")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ready := make(chan bool, 1)
				go func() {
					deadline := time.Now().Add(10 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(marker); err == nil {
							cancel()
							ready <- true
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
					ready <- false
				}()
				result, err := en.Run(ctx, engine.Request{Operation: "merge", Target: "feature", Interactive: interactive})
				if !<-ready || !errors.Is(err, context.Canceled) || result.Status != "interrupted" || result.Integrated {
					t.Fatal(result, err)
				}
				time.Sleep(1100 * time.Millisecond)
				if _, err = os.Stat(filepath.Join(g.Dir, "late-file")); !os.IsNotExist(err) {
					t.Fatal("managed child survived resolver cancellation", err)
				}
				raw, err := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
				if err != nil {
					t.Fatal(err)
				}
				support, err := os.ReadFile(filepath.Join(g.Dir, "support.txt"))
				if err != nil {
					t.Fatal(err)
				}
				if interactive {
					if !bytes.Contains(raw, []byte("<<<<<<<")) || string(support) != "clean\n" {
						t.Fatal("tracked state not restored", string(raw), string(support))
					}
				} else if string(raw) != "unapproved\n" || string(support) != "unapproved\n" {
					t.Fatal("noninteractive partial edits lost", string(raw), string(support))
				}
			})
		}
	}
}

func TestEveryNativeRebaseStopGetsAnIndependentProductionSession(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, fixture, closeFixture := setup(t, provider, read("file.txt"), write("file.txt", "first\n"), finish(.95), read("file.txt"), write("file.txt", "final\n"), finish(.95))
			defer closeFixture()
			testutil.Must(t, g, "merge", "--abort")
			testutil.Commit(t, g, "file.txt", "second\n", "second replayed commit")
			en, err := engine.New(context.Background(), g, c, New(c))
			if err != nil {
				t.Fatal(err)
			}
			result, err := en.Run(context.Background(), engine.Request{Operation: "rebase", Target: "feature"})
			if err != nil || !result.Integrated || result.ResolverCalls != 2 || len(fixture.requests) != 6 {
				t.Fatal(result, err, len(fixture.requests))
			}
			raw, err := os.ReadFile(filepath.Join(result.Directory, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			var run map[string]any
			if err = json.Unmarshal(raw, &run); err != nil {
				t.Fatal(err)
			}
			sessions := run["sessions"].([]any)
			if len(sessions) != 2 || sessions[0].(map[string]any)["artifacts"] == sessions[1].(map[string]any)["artifacts"] {
				t.Fatal(sessions)
			}
			for _, entry := range sessions {
				session := entry.(map[string]any)
				if session["model_calls"] != float64(3) || session["status"] != "resolved" {
					t.Fatal(session)
				}
			}
			if testutil.Must(t, g, "show", "HEAD:file.txt") != "final" || testutil.Must(t, g, "show", "HEAD^:file.txt") != "first" || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("replay results lost")
			}
		})
	}
}

func TestFinishAllowsRepairOfChangedNativeBinaryResources(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, _, _, closeFixture := setup(t, provider)
			closeFixture()
			g := testutil.Repo(t)
			testutil.Commit(t, g, "file.txt", "base\n", "base")
			testutil.Write(t, g, "resource.dat", "old\x00resource")
			testutil.Must(t, g, "add", "resource.dat")
			common := testutil.Commit(t, g, "file.txt", "base\n", "shared resource")
			testutil.Must(t, g, "checkout", "-b", "feature", common)
			testutil.Commit(t, g, "file.txt", "feature\n", "feature text")
			testutil.Must(t, g, "checkout", "main")
			testutil.Write(t, g, "resource.dat", "Git\x00resource")
			testutil.Commit(t, g, "file.txt", "main\n", "main text and resource")
			testutil.Must(t, g, "add", "resource.dat")
			testutil.Must(t, g, "commit", "--amend", "--no-edit")
			fixture := &modelFixture{steps: []step{read("file.txt"), read("resource.dat"), write("resource.dat", "old\x00resource"), write("file.txt", "combined\n"), finish(.95), write("resource.dat", "Git\x00resource"), finish(.95)}}
			server := httptest.NewServer(fixture)
			defer server.Close()
			c.Endpoint = server.URL + "/v1/chat/completions"
			if provider == "ollama" {
				c.Endpoint = server.URL + "/api/chat"
			}
			testutil.Must(t, g, "checkout", "feature")
			en, err := engine.New(context.Background(), g, c, New(c))
			if err != nil {
				t.Fatal(err)
			}
			result, err := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main"})
			if err != nil || !result.Integrated {
				t.Fatal(result, err)
			}
			raw, err := os.ReadFile(filepath.Join(g.Dir, "resource.dat"))
			if err != nil || string(raw) != "Git\x00resource" {
				t.Fatal("native binary changed", err)
			}
			requests, _ := json.Marshal(fixture.requests)
			if !strings.Contains(string(requests), "binary or non-UTF-8 proposal") {
				t.Fatal("finish rejection not fed back")
			}
		})
	}
}
