// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestTransportOptionsAndCredentialSelection(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			t.Setenv("OLLAMA_API_KEY", "ambient-must-not-be-used")
			t.Setenv("OPENAI_API_KEY", "wrong-ambient")
			t.Setenv("SELECTED_TOKEN", "selected-token")
			c.APIKeyEnv = "SELECTED_TOKEN"
			key := "explicit-key"
			c.APIKey = &key
			if provider == "openai" {
				c.RequestOptions = map[string]any{"temperature": .2, "n": 2, "chat_template_kwargs": map[string]any{"enable_thinking": false}}
			} else {
				c.RequestOptions = map[string]any{"options": map[string]any{"temperature": .2, "num_predict": 512}, "think": true, "keep_alive": "5m", "logprobs": true, "top_logprobs": 2}
			}
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for i, req := range f.requests {
				if f.auth[i] != "Bearer explicit-key" {
					t.Fatal(f.auth)
				}
				if provider == "openai" {
					if req["n"] != float64(2) || req["chat_template_kwargs"] == nil {
						t.Fatal(req)
					}
				} else {
					opts, _ := req["options"].(map[string]any)
					if req["think"] != true || opts["num_predict"] != float64(512) || req["keep_alive"] != "5m" {
						t.Fatal(req)
					}
				}
			}
			files, _ := filepath.Glob(filepath.Join(r.Directory, "agent-*", "*.json"))
			for _, p := range files {
				b, _ := os.ReadFile(p)
				if strings.Contains(string(b), key) || strings.Contains(string(b), "wrong-ambient") {
					t.Fatal("credential persisted", p)
				}
			}
		})
	}
}
func TestTLSVerificationAndCustomCA(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, trusted := range []bool{false, true} {
			t.Run(provider+map[bool]string{true: "-trusted", false: "-untrusted"}[trusted], func(t *testing.T) {
				c, g, _, close := setup(t, provider)
				close()
				f := &modelFixture{steps: []step{read("file.txt"), write("file.txt", "combined\n"), finish(.95)}}
				server := httptest.NewTLSServer(f)
				defer server.Close()
				c.Endpoint = server.URL + "/v1/chat/completions"
				if provider == "ollama" {
					c.Endpoint = server.URL + "/api/chat"
				}
				if trusted {
					cert, e := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
					if e != nil {
						t.Fatal(e)
					}
					path := filepath.Join(t.TempDir(), "ca.pem")
					if e = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); e != nil {
						t.Fatal(e)
					}
					c.CABundle = &path
				}
				r, e := runEngine(t, c, g)
				if trusted && (e != nil || !r.Integrated) {
					t.Fatal(r, e)
				}
				if !trusted && (e == nil || r.Integrated) {
					t.Fatal("TLS bypassed", r, e)
				}
			})
		}
	}
}
func TestProductionTelemetryPolicy(t *testing.T) {
	var external atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { external.Add(1); w.WriteHeader(200) }))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("GENKIT_TELEMETRY_SERVER", collector.URL)
	t.Setenv("GENKIT_ENV", "prod")
	c, g, _, close := setup(t, "openai", read("file.txt"), write("file.txt", "combined\n"), finish(.95))
	defer close()
	r, e := runEngine(t, c, g)
	if e != nil || !r.Integrated || external.Load() != 0 {
		t.Fatal(r, e, external.Load())
	}
	for _, setting := range []string{"GENKIT_ENV", "GENKIT_REFLECTION_ENABLED"} {
		t.Run(setting, func(t *testing.T) {
			value := "true"
			if setting == "GENKIT_ENV" {
				value = "dev"
			}
			t.Setenv(setting, value)
			c, g, f, close := setup(t, "openai", read("file.txt"))
			defer close()
			r, e := runEngine(t, c, g)
			if e == nil || r.Integrated || len(f.requests) != 0 || external.Load() != 0 {
				t.Fatal("unsafe initialization", r, e)
			}
		})
	}
}
func TestLargeOutputArtifactRetrieval(t *testing.T) {
	steps := []step{read("file.txt"), command(`python3 -c "import sys; sys.stdout.write('x'*1400000+'LATE_EVIDENCE'+'y'*10000)"`), {name: "read_artifact", args: map[string]any{"path": "command-000002.stdout", "offset": 1400000}}, write("file.txt", "combined\n"), finish(.95)}
	c, g, f, close := setup(t, "openai", steps...)
	defer close()
	c.ToolOutputBytes = 1024
	r, e := runEngine(t, c, g)
	if e != nil || !r.Integrated {
		t.Fatal(r, e)
	}
	paths, _ := filepath.Glob(filepath.Join(r.Directory, "agent-*", "command-000002.stdout"))
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	raw, _ := os.ReadFile(paths[0])
	if len(raw) != 1410013 {
		t.Fatal("retention length", len(raw))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(f.requests)
	if !strings.Contains(string(b), "LATE_EVIDENCE") {
		t.Fatal("retrieved evidence not in native message history")
	}
}
func TestMultiFileInvestigationAndSupportingEdit(t *testing.T) {
	c, g, _, close := setup(t, "openai")
	close()
	testutil.Must(t, g, "merge", "--abort")
	base := testutil.Must(t, g, "rev-parse", "feature~1")
	testutil.Must(t, g, "checkout", "-b", "base", base)
	testutil.Commit(t, g, "b.txt", "base\n", "base B")
	testutil.Must(t, g, "checkout", "-b", "left")
	testutil.Commit(t, g, "file.txt", "left A\n", "left A")
	testutil.Commit(t, g, "b.txt", "left B\n", "left B")
	testutil.Must(t, g, "checkout", "-b", "right", "base")
	testutil.Commit(t, g, "file.txt", "right A\n", "right A")
	testutil.Commit(t, g, "b.txt", "right B\n", "right B")
	fixture := &modelFixture{steps: []step{read("file.txt"), read("b.txt"), command("git log --oneline --all"), write("file.txt", "combined A\n"), write("b.txt", "combined B\n"), write("support.txt", "Both branch intentions are preserved.\n"), command("test -f support.txt"), finish(.95)}}
	server := httptest.NewServer(fixture)
	defer server.Close()
	c.Endpoint = server.URL + "/v1/chat/completions"
	en, e := engine.New(context.Background(), g, c, New(c))
	if e != nil {
		t.Fatal(e)
	}
	r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "left"})
	if e != nil || !r.Integrated || r.ResolverCalls != 1 {
		t.Fatal(r, e)
	}
	if testutil.Must(t, g, "show", "HEAD:support.txt") != "Both branch intentions are preserved." {
		t.Fatal("supporting edit")
	}
}
func TestProviderProbeBothProtocols(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			var count atomic.Int32
			markerPattern := regexp.MustCompile(`ez-[a-f0-9]{32}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Error("bad probe JSON")
				}
				b, _ := json.Marshal(req)
				marker := markerPattern.FindString(string(b))
				current := count.Add(1)
				response := step{text: marker}
				if current == 2 {
					response = step{name: "compatibility_echo", args: map[string]any{"value": marker}}
					if provider == "ollama" {
						response.thinking = "Native probe reasoning"
					}
				}
				if provider == "ollama" && current == 3 {
					messages := req["messages"].([]any)
					assistant := messages[1].(map[string]any)
					toolResult := messages[2].(map[string]any)
					if assistant["thinking"] != "Native probe reasoning" || assistant["content"] != "" || toolResult["tool_name"] != "compatibility_echo" {
						t.Error("Native reasoning/tool history lost", messages)
					}
				}
				localFixture := &modelFixture{steps: []step{response}}
				body, _ := json.Marshal(req)
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				localFixture.ServeHTTP(w, r)
			}))
			defer server.Close()
			c := config.Defaults()
			c.Provider = provider
			c.Model = "fixture"
			c.Endpoint = server.URL + "/v1/chat/completions"
			if provider == "ollama" {
				c.Endpoint = server.URL + "/api/chat"
			}
			report, ok := Probe(context.Background(), c)
			if !ok || count.Load() != 3 || !strings.Contains(string(report), `"status":"compatible"`) {
				t.Fatal(ok, count.Load(), string(report))
			}
		})
	}
}

func TestAnonymousProvidersNeverUseAmbientSDKCredentials(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "wrong-openai-key")
			t.Setenv("OLLAMA_API_KEY", "wrong-ollama-key")
			t.Setenv("NO_SELECTED_TOKEN", "")
			c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			c.APIKeyEnv = "NO_SELECTED_TOKEN"
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			expected := ""
			if provider == "openai" {
				expected = "Bearer local-no-key"
			}
			for _, auth := range f.auth {
				if auth != expected {
					t.Fatal("ambient credential or reference sentinel mismatch", auth)
				}
			}
		})
	}
}
func TestNestedSummarizationCannotExceedActualRequestBudget(t *testing.T) {
	steps := []step{read("file.txt")}
	for i := 0; i < 16; i++ {
		steps = append(steps, command("printf evidence"))
	}
	steps = append(steps, write("file.txt", "combined\n"), finish(.95))
	c, g, f, close := setup(t, "openai", steps...)
	defer close()
	c.AgentMaxTurns = 20
	f.tokenInput = c.ModelContextTokens - c.ModelMaxOutputTokens + 1
	r, e := runEngine(t, c, g)
	if e == nil || r.Integrated || len(f.requests) != 20 || f.summaries < 1 {
		t.Fatal("nested requests bypassed budget", r, e, len(f.requests), f.summaries)
	}
}

func TestProviderHTTPFailuresAreSingleAttemptAndFiltered(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, status := range []int{302, 401, 429, 500} {
			t.Run(provider+"/"+fmt.Sprint(status), func(t *testing.T) {
				c, _, f, close := setup(t, provider)
				defer close()
				secret := "fake-secret-never-persist"
				c.APIKey = &secret
				f.secret = secret
				f.status = status
				report, ok := Probe(context.Background(), c)
				if ok || len(f.requests) != 1 || strings.Contains(string(report), secret) {
					t.Fatal(ok, len(f.requests), string(report))
				}
			})
		}
	}
}
func TestReferenceTokenOptionsAreNotRestrictedByAdapter(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, kind := range []string{"larger", "null", "unbounded"} {
			if provider == "openai" && kind == "unbounded" {
				continue
			}
			t.Run(provider+"/"+kind, func(t *testing.T) {
				c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
				defer close()
				var expected any = c.ModelMaxOutputTokens + 1
				if kind == "null" {
					expected = nil
				}
				if kind == "unbounded" {
					expected = -1
				}
				key := "max_completion_tokens"
				if provider == "openai" {
					c.RequestOptions = map[string]any{key: expected}
				} else {
					key = "num_predict"
					c.RequestOptions = map[string]any{"options": map[string]any{key: expected, "num_ctx": c.ModelContextTokens + 1}}
				}
				r, e := runEngine(t, c, g)
				if e != nil || !r.Integrated {
					t.Fatal(r, e)
				}
				for _, request := range f.requests {
					options := request
					if provider == "ollama" {
						options = request["options"].(map[string]any)
					}
					value, exists := options[key]
					// Native Ollama omits null generation fields, matching the reference.
					if expected == nil && provider == "ollama" {
						if exists {
							t.Fatal("null not omitted", value)
						}
						continue
					}
					if !exists || fmt.Sprint(value) != fmt.Sprint(expected) {
						t.Fatal("reference option changed", key, value, expected)
					}
				}
			})
		}
	}
}

func TestReportedUsageIsEvidenceNotCustomTokenAdmission(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			f.tokenInput = c.ModelContextTokens + 1
			f.tokenOutput = c.ModelMaxOutputTokens + 1
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
		})
	}
}

func TestRequestBytesAreNotTreatedAsTokens(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
			defer close()
			c.ModelContextTokens = 4096
			c.ModelMaxOutputTokens = 512
			r, e := runEngine(t, c, g)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			data, e := json.Marshal(f.requests[0])
			if e != nil || len(data) <= c.ModelContextTokens-c.ModelMaxOutputTokens {
				t.Fatal("fixture did not exceed removed byte-to-token ceiling", len(data), e)
			}
		})
	}
}
