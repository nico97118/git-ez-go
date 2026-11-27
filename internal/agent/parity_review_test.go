// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/testutil"
)

// A scripted repair alone is insufficient: inspect the next native request to
// prove that repository discovery, Git investigation and rejection feedback were
// actually delivered to the model, for both supported protocols.
func TestDiscoveryHistoryAndRejectedFinishReachNextModelRequest(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			c, g, fixture, close := setup(t, provider,
				step{name: "list_files", args: map[string]any{"dirPath": "."}},
				command("git grep -n main HEAD -- file.txt; git log -1 --format=%s"),
				read("file.txt"), write("file.txt", "<<<<<<< unfinished\n"), finish(.95),
				write("file.txt", "combined\n"), finish(.95))
			defer close()
			result, err := runEngine(t, c, g)
			if err != nil || !result.Integrated || result.ResolverCalls != 1 {
				t.Fatal(result, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.requests) != 7 {
				t.Fatal("unexpected physical request count", len(fixture.requests))
			}
			outputs := func(index int) string {
				var out []string
				for _, raw := range fixture.requests[index]["messages"].([]any) {
					message := raw.(map[string]any)
					if message["role"] == "tool" {
						encoded, e := json.Marshal(message["content"])
						if e != nil {
							t.Fatal(e)
						}
						out = append(out, string(encoded))
					}
				}
				return strings.Join(out, "\n")
			}
			if list := outputs(1); !strings.Contains(list, "file.txt") || !strings.Contains(list, "isDirectory") {
				t.Fatal("listing result absent from next request", list)
			}
			if history := outputs(2); !strings.Contains(history, "HEAD:file.txt:1:main") {
				t.Fatal("actual Git search output absent from next request", history)
			}
			if feedback := outputs(5); !strings.Contains(strings.ToLower(feedback), "conflict markers") {
				t.Fatal("finish rejection not delivered before repair", feedback)
			}
			events, e := filepath.Glob(filepath.Join(result.Directory, "agent-*", "event-*.json"))
			if e != nil {
				t.Fatal(e)
			}
			started, finished, rejected := 0, 0, 0
			for _, path := range events {
				raw, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				var event map[string]any
				if e = json.Unmarshal(raw, &event); e != nil {
					t.Fatal(e)
				}
				if event["event"] == "tool_started" {
					started++
				}
				if event["event"] == "tool_result" {
					finished++
					data := event["data"].(map[string]any)
					if data["name"] == "finish" && data["failed"] == true {
						rejected++
					}
				}
			}
			if started != 7 || finished != 7 || rejected != 1 {
				t.Fatal("missing or duplicate tool/rejection evidence", started, finished, rejected)
			}
			if testutil.Must(t, g, "show", "HEAD:file.txt") != "combined" || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("proposal did not integrate cleanly")
			}
		})
	}
}
