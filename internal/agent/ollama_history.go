// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/firebase/genkit/go/ai"
)

type ollamaHistoryKey struct{}

// Genkit's Ollama converter currently flattens reasoning into assistant content.
// Carry the authoritative request only for this model call so the transport can
// preserve Ollama's separate thinking field. No history or compression is owned here.
func ollamaHistoryContext(ctx context.Context, request *ai.ModelRequest) context.Context {
	return context.WithValue(ctx, ollamaHistoryKey{}, request)
}

func restoreOllamaThinking(ctx context.Context, messages []any) error {
	request, _ := ctx.Value(ollamaHistoryKey{}).(*ai.ModelRequest)
	if request == nil {
		return nil
	}
	for i, source := range request.Messages {
		if source.Role != ai.RoleModel {
			continue
		}
		var text, thinking, flattened strings.Builder
		for _, part := range source.Content {
			if part.IsReasoning() {
				thinking.WriteString(part.Text)
				flattened.WriteString(part.Text)
			}
			if part.IsText() {
				text.WriteString(part.Text)
				flattened.WriteString(part.Text)
			}
		}
		if thinking.Len() == 0 {
			continue
		}
		if len(messages) != len(request.Messages) {
			return fail("Ollama reasoning history does not match model request")
		}
		target, ok := messages[i].(map[string]any)
		content, _ := target["content"].(string)
		if !ok || target["role"] != "assistant" || content != flattened.String() {
			return fail("Ollama reasoning content does not match model request")
		}
		target["content"] = text.String()
		target["thinking"] = thinking.String()
	}
	return nil
}

// Genkit also joins batched tool outputs into one unnamed string. Ollama expects
// independent named tool messages, as in the reference native provider.
func restoreOllamaTools(ctx context.Context, messages []any) ([]any, error) {
	request, _ := ctx.Value(ollamaHistoryKey{}).(*ai.ModelRequest)
	if request == nil {
		return messages, nil
	}
	if len(messages) != len(request.Messages) {
		return nil, fail("Ollama tool history does not match model request")
	}
	out := make([]any, 0, len(messages))
	for i, source := range request.Messages {
		toolOnly := source.Role == ai.RoleTool && len(source.Content) > 0
		for _, part := range source.Content {
			toolOnly = toolOnly && part.IsToolResponse()
		}
		if !toolOnly {
			out = append(out, messages[i])
			continue
		}
		for _, part := range source.Content {
			data, err := json.Marshal(part.ToolResponse.Output)
			if err != nil {
				return nil, fail("Cannot encode Ollama tool result")
			}
			out = append(out, map[string]any{"role": "tool", "tool_name": part.ToolResponse.Name, "content": string(data)})
		}
	}
	return out, nil
}
