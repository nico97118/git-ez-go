// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestNativeRebaseAndCherryPickWithProductionResolver(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, op := range []string{"rebase", "cherry-pick"} {
			t.Run(provider+"/"+op, func(t *testing.T) {
				c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", "combined\n"), finish(.95))
				defer close()
				testutil.Must(t, g, "merge", "--abort")
				en, e := engine.New(context.Background(), g, c, New(c))
				if e != nil {
					t.Fatal(e)
				}
				r, e := en.Run(context.Background(), engine.Request{Operation: op, Target: "feature"})
				if e != nil || !r.Integrated || r.ResolverCalls != 1 || len(f.requests) != 3 {
					t.Fatal(r, e, len(f.requests))
				}
				raw, e := os.ReadFile(filepath.Join(r.Directory, "run.json"))
				if e != nil {
					t.Fatal(e)
				}
				var view map[string]any
				_ = json.Unmarshal(raw, &view)
				sessions, ok := view["sessions"].([]any)
				if !ok || len(sessions) != 1 || sessions[0].(map[string]any)["model_calls"] != float64(3) {
					t.Fatal("resolver evidence not retained by engine journal")
				}
			})
		}
	}
}
func TestProductionResolverNeverUsedForCleanOrManualCompletion(t *testing.T) {
	for _, kind := range []string{"clean", "manual"} {
		t.Run(kind, func(t *testing.T) {
			c, g, f, close := setup(t, "openai", read("file.txt"))
			defer close()
			if kind == "clean" {
				testutil.Must(t, g, "merge", "--abort")
				testutil.Must(t, g, "checkout", "-b", "behind", "feature~1")
			} else {
				testutil.Write(t, g, "file.txt", "manual\n")
				testutil.Must(t, g, "add", "file.txt")
			}
			en, e := engine.New(context.Background(), g, c, New(c))
			if e != nil {
				t.Fatal(e)
			}
			q := engine.Request{Operation: "merge", Target: "feature"}
			if kind == "manual" {
				q.Continue = true
				q.Target = ""
			}
			r, e := en.Run(context.Background(), q)
			if e != nil || r.ResolverCalls != 0 || len(f.requests) > 0 {
				t.Fatal(r, e)
			}
		})
	}
}

func TestSemanticConflictInvestigationRepairAndIndependentChecks(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, operation := range []string{"merge", "rebase", "cherry-pick"} {
			t.Run(provider+"/"+operation, func(t *testing.T) {

				c, _, _, close := setup(t, provider)
				close()
				g := testutil.Repo(t)
				testutil.Write(t, g, "calc.py", "from settings import TAX, DISCOUNT\ndef total(n):\n    return n * 10\n")
				testutil.Write(t, g, "settings.py", "TAX, DISCOUNT = 0.0, 0.0\n")
				testutil.Must(t, g, "add", ".")
				testutil.Must(t, g, "commit", "-m", "base")
				testutil.Must(t, g, "checkout", "-b", "discount")
				testutil.Write(t, g, "calc.py", "from settings import TAX, DISCOUNT\ndef total(n):\n    return n * 10 * (1 - DISCOUNT if n >= 10 else 1)\n")
				testutil.Write(t, g, "settings.py", "TAX, DISCOUNT = 0.0, 0.1\n")
				testutil.Must(t, g, "commit", "-am", "bulk discount")
				testutil.Must(t, g, "checkout", "main")
				testutil.Must(t, g, "checkout", "-b", "tax")
				testutil.Write(t, g, "calc.py", "from settings import TAX, DISCOUNT\ndef total(n):\n    return n * 10 * (1 + TAX)\n")
				testutil.Write(t, g, "settings.py", "TAX, DISCOUNT = 0.2, 0.0\n")
				testutil.Must(t, g, "commit", "-am", "tax")
				assertion := "from calc import total; from math import isclose; assert isclose(total(10),108); assert isclose(total(1),12)"
				check := "python3 -B -c '" + assertion + "'"
				f := &modelFixture{steps: []step{read("calc.py"), read("settings.py"), command("git show :1:calc.py; git log --oneline --all"), write("calc.py", "from settings import TAX, DISCOUNT\ndef total(n):\n    return n * 10 * (1 + TAX)\n"), write("settings.py", "TAX, DISCOUNT = 0.2, 0.1\n"), command(check), write("calc.py", "from settings import TAX, DISCOUNT\ndef total(n):\n    return n * 10 * (1 - DISCOUNT if n >= 10 else 1) * (1 + TAX)\n"), command(check), write("resolution.txt", "Bulk discount applies before tax; ordinary quantities keep their full price.\n"), finish(.95)}}
				server := httptest.NewServer(f)
				defer server.Close()
				c.Endpoint = server.URL + "/v1/chat/completions"
				if provider == "ollama" {
					c.Endpoint = server.URL + "/api/chat"
				}
				en, e := engine.New(context.Background(), g, c, New(c))
				if e != nil {
					t.Fatal(e)
				}
				r, e := en.Run(context.Background(), engine.Request{Operation: operation, Target: "discount", Validation: []string{"python3", "-B", "-c", assertion}})
				if e != nil || !r.Integrated || r.ResolverCalls != 1 {
					t.Fatal(r, e)
				}
				data, _ := json.Marshal(f.requests)
				if !strings.Contains(string(data), `exit_code\":1`) {
					t.Fatal("failed independent check was not returned as native tool feedback")
				}
				if testutil.Must(t, g, "status", "--porcelain") != "" {
					t.Fatal("generated files or unstaged proposals remain")
				}
			})
		}
	}
}

func TestProductionResolverInteractiveRevisionAndValidationFeedback(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, kind := range []string{"revision", "validation", "failed-revision"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				first := "first proposal\n"
				if kind == "validation" {
					first = "bad\n"
				}
				last := finish(.95)
				if kind == "failed-revision" {
					last = step{text: "Stopped without finish"}
				}
				c, g, f, close := setup(t, provider, read("file.txt"), write("file.txt", first), finish(.95), read("file.txt"), write("file.txt", "good\n"), last)
				defer close()
				testutil.Must(t, g, "merge", "--abort")
				en, e := engine.New(context.Background(), g, c, New(c))
				if e != nil {
					t.Fatal(e)
				}
				en.Out = io.Discard
				q := engine.Request{Operation: "merge", Target: "feature", Interactive: true}
				en.In = strings.NewReader("r\nretain both intents\na\n")
				expected := "retain both intents"
				if kind == "validation" {
					en.In = strings.NewReader("a\n")
					q.Validation = []string{"/bin/sh", "-c", "printf independent-check-feedback; grep -qx good file.txt"}
					expected = "independent-check-feedback"
				}
				r, e := en.Run(context.Background(), q)
				if (kind != "failed-revision" && (e != nil || !r.Integrated)) || (kind == "failed-revision" && (e == nil || r.Integrated)) || r.ResolverCalls != 2 {
					t.Fatal(r, e)
				}
				wire, _ := json.Marshal(f.requests[3])
				if !strings.Contains(string(wire), expected) || !strings.Contains(string(wire), "Previous") {
					t.Fatal("review feedback/history absent from authoritative mission", string(wire))
				}
				raw, e := os.ReadFile(filepath.Join(r.Directory, "run.json"))
				if e != nil {
					t.Fatal(e)
				}
				var run map[string]any
				if e = json.Unmarshal(raw, &run); e != nil {
					t.Fatal(e)
				}
				reviews := run["reviews"].([]any)
				if len(reviews) != 2 || reviews[0].(map[string]any)["session"] == reviews[1].(map[string]any)["session"] {
					t.Fatal("resolver sessions not separately linked", reviews)
				}
			})
		}
	}
}

func TestAuditRetainsEffectiveConfigurationWithoutDuplicateHistory(t *testing.T) {
	c, g, _, close := setup(t, "openai", read("file.txt"), write("file.txt", "combined\n"), finish(.95))
	defer close()
	secret := "synthetic-audit-config-secret"
	c.APIKey = &secret
	r, e := runEngine(t, c, g)
	if e != nil || !r.Integrated {
		t.Fatal(r, e)
	}
	sessions, e := filepath.Glob(filepath.Join(r.Directory, "agent-*"))
	if e != nil || len(sessions) != 1 {
		t.Fatal(sessions, e)
	}
	for _, name := range []string{"config.json", "instructions.txt", "mission.json", "session.json"} {
		raw, e := os.ReadFile(filepath.Join(sessions[0], name))
		if e != nil || strings.Contains(string(raw), secret) {
			t.Fatal(name, string(raw), e)
		}
	}
	events, e := filepath.Glob(filepath.Join(sessions[0], "event-*.json"))
	if e != nil {
		t.Fatal(e)
	}
	wireRequests, wireResponses := 0, 0
	for _, name := range events {
		raw, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		var event map[string]any
		if e = json.Unmarshal(raw, &event); e != nil {
			t.Fatal(e)
		}
		if event["event"] == "history" {
			t.Fatal("duplicated complete history persisted")
		}
		if event["event"] == "model_request" {
			wireRequests++
		}
		if event["event"] == "model_http_response" {
			wireResponses++
		}
		if event["event"] == "model_result" {
			if event["data"].(map[string]any)["response"] != nil {
				t.Fatal("normalized response duplicated wire payload")
			}
		}
	}
	if wireRequests != 3 || wireResponses != 3 {
		t.Fatal("authoritative wire evidence missing", wireRequests, wireResponses)
	}
}

func TestAgentSessionLinkPrecedesModelRequest(t *testing.T) {
	c, g, f, close := setup(t, "openai", read("file.txt"), write("file.txt", "combined\n"), finish(.95))
	defer close()
	directory := filepath.Join(t.TempDir(), "run")
	if e := os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	updates := []resolver.Evidence{}
	head := testutil.Must(t, g, "rev-parse", "HEAD")
	target := testutil.Must(t, g, "rev-parse", "feature")
	stop := resolver.Stop{Operation: "merge", Worktree: g.Dir, Branch: "main", BeforeOID: head, HeadOID: head, Target: resolver.Pin{OID: target, Reference: "refs/heads/feature"}, Environment: func() []string { return append([]string{}, g.Env...) }, ArtifactDirectory: directory, Conflicts: []resolver.Conflict{{Path: "file.txt"}}, Retain: func(ev resolver.Evidence) error {
		if len(updates) == 0 && len(f.requests) != 0 {
			t.Fatal("model invoked before initial session publication")
		}
		updates = append(updates, ev)
		return nil
	}}
	_, e := New(c).Resolve(context.Background(), stop)
	if e != nil {
		t.Fatal(e)
	}
	if len(updates) != 2 || updates[0].Status != "running" || updates[0].ModelCalls != 0 || updates[1].Status != "resolved" || updates[1].ModelCalls != 3 || updates[0].Directory != updates[1].Directory {
		t.Fatal("session lifecycle not inspectable", updates)
	}
}
