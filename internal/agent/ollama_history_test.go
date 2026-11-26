// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/ai"
)

func TestOllamaReasoningRemainsSeparateAcrossNativeToolCalls(t *testing.T) {
	first := read("file.txt")
	first.thinking = "Private reasoning: investigate both branches."
	first.text = "Visible assistant text."
	c, g, fixture, close := setup(t, "ollama", first, write("file.txt", "combined\n"), finish(.95))
	defer close()
	c.RequestOptions = map[string]any{"reasoning": true}
	result, err := runEngine(t, c, g)
	if err != nil || !result.Integrated {
		t.Fatal(result, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 3 {
		t.Fatal(len(fixture.requests))
	}
	for _, request := range fixture.requests[1:] {
		found := false
		for _, raw := range request["messages"].([]any) {
			message := raw.(map[string]any)
			if message["thinking"] == first.thinking {
				found = true
				if message["role"] != "assistant" || message["content"] != first.text || len(message["tool_calls"].([]any)) != 1 {
					t.Fatal(message)
				}
			}
			content, _ := message["content"].(string)
			if strings.Contains(content, first.thinking) {
				t.Fatal("Thinking was flattened into assistant content")
			}
		}
		if !found {
			t.Fatal("Native thinking history was lost")
		}
	}
	files, err := filepath.Glob(filepath.Join(result.Directory, "agent-*", "event-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatal(files, err)
	}
	found := false
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(string(raw), first.thinking)
	}
	if !found {
		t.Fatal("Thinking evidence lost")
	}
}

func TestOllamaReasoningHistoryMismatchFailsExplicitly(t *testing.T) {
	request := &ai.ModelRequest{Messages: []*ai.Message{{Role: ai.RoleModel, Content: []*ai.Part{ai.NewReasoningPart("reason", nil), ai.NewTextPart("answer")}}}}
	ctx := ollamaHistoryContext(context.Background(), request)
	for _, messages := range [][]any{nil, {map[string]any{"role": "assistant", "content": "different"}}, {map[string]any{"role": "user", "content": "reasonanswer"}}} {
		if restoreOllamaThinking(ctx, messages) == nil {
			t.Fatal("Mismatched provider history accepted", messages)
		}
	}
	if err := restoreOllamaThinking(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaBatchedToolResultsRemainNamedAndSeparate(t *testing.T) {
	first := step{batch: []step{read("file.txt"), command("printf diagnostic")}}
	c, g, fixture, close := setup(t, "ollama", first, write("file.txt", "combined\n"), finish(.95))
	defer close()
	result, err := runEngine(t, c, g)
	if err != nil || !result.Integrated {
		t.Fatal(result, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	names := map[string]int{}
	for _, raw := range fixture.requests[1]["messages"].([]any) {
		m := raw.(map[string]any)
		if m["role"] != "tool" {
			continue
		}
		name, _ := m["tool_name"].(string)
		names[name]++
		content, _ := m["content"].(string)
		if !json.Valid([]byte(content)) {
			t.Fatal("Tool results were concatenated", m)
		}
	}
	if names["read_file"] != 1 || names["execute"] != 1 || len(names) != 2 {
		t.Fatal(names)
	}
}
