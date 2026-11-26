// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/journal"
)

// Probe validates only the real framework/provider native protocol. It exposes
// one harmless echo tool and never opens a Git repository or runs commands.
func Probe(ctx context.Context, c config.Config) (report json.RawMessage, compatible bool) {
	checks := []any{}
	view := map[string]any{"created_at": time.Now().UTC().Format(time.RFC3339Nano), "provider": c.Provider, "model": c.Model, "base_url": baseURL(c.Endpoint, c.Provider), "status": "failed", "note": "Only protocol compatibility is tested; no Git or filesystem tools are exposed."}
	probeConfig := c
	probeConfig.AgentMaxTurns = 3 // fixed reference probe protocol, independent of resolver turn limits
	s := &session{config: probeConfig}
	finish := func(e error) (json.RawMessage, bool) {
		if s.closeTransport != nil {
			s.closeTransport()
		}
		view["checks"] = checks
		if e != nil {
			statusCode := any(nil)
			if s.lastStatus >= 300 {
				statusCode = s.lastStatus
			}
			view["error"] = map[string]any{"type": fmt.Sprintf("%T", e), "status_code": statusCode, "message": "Provider protocol compatibility failed"}
		}
		view["model_calls"] = s.calls
		b, err := journal.FilterJSON(view, c.ResolvedAPIKey(nil))
		if err != nil {
			return json.RawMessage(`{"status":"failed","error":{"type":"capture_error"}}`), false
		}
		return b, e == nil
	}
	if e := c.Validate(); e != nil {
		return finish(e)
	}
	g, ref, e := s.model(ctx)
	if e != nil {
		return finish(e)
	}
	marker := "ez-" + journal.ID()
	chat, e := genkit.Generate(ctx, g, ai.WithModel(ref), ai.WithPrompt("Reply with exactly "+marker), ai.WithMaxTurns(1))
	if e != nil {
		return finish(e)
	}
	ok := strings.Contains(chat.Text(), marker) && len(chat.ToolRequests()) == 0
	checks = append(checks, map[string]any{"name": "chat", "ok": ok, "response": chat.Message})
	if !ok {
		return finish(fail("Chat marker mismatch"))
	}
	echo := genkit.DefineTool(g, "compatibility_echo", "Echo a harmless compatibility marker.", func(_ *ai.ToolContext, in struct {
		Value string `json:"value"`
	}) (string, error) {
		return in.Value, nil
	})
	initial := ai.NewUserTextMessage("Call compatibility_echo with value " + marker + ". After its result, reply with exactly the returned value.")
	called, e := genkit.Generate(ctx, g, ai.WithModel(ref), ai.WithMessages(initial), ai.WithTools(echo), ai.WithReturnToolRequests(true))
	if e != nil {
		return finish(e)
	}
	calls := called.ToolRequests()
	ok = len(calls) == 1 && !s.missingWireToolReference
	if ok {
		request := calls[0].ToolRequest
		raw, _ := json.Marshal(request.Input)
		var args map[string]any
		_ = json.Unmarshal(raw, &args)
		ok = request.Name == "compatibility_echo" && len(args) == 1 && args["value"] == marker && request.Ref != ""
	}
	checks = append(checks, map[string]any{"name": "tool_call", "ok": ok, "response": called.Message})
	if !ok {
		return finish(fail("Native tool call mismatch"))
	}
	answer := ai.NewMessage(ai.RoleTool, nil, ai.NewToolResponsePart(&ai.ToolResponse{Name: "compatibility_echo", Ref: calls[0].ToolRequest.Ref, Output: marker}))
	history := []*ai.Message{initial, called.Message, answer}
	finalCtx := ctx
	if c.Provider == "ollama" {
		finalCtx = ollamaHistoryContext(ctx, &ai.ModelRequest{Messages: history})
	}
	final, e := genkit.Generate(finalCtx, g, ai.WithModel(ref), ai.WithMessages(history...), ai.WithTools(echo), ai.WithReturnToolRequests(true))
	if e != nil {
		return finish(e)
	}
	ok = len(final.ToolRequests()) == 0 && strings.Contains(final.Text(), marker)
	checks = append(checks, map[string]any{"name": "tool_result_roundtrip", "ok": ok, "response": final.Message})
	if !ok {
		return finish(fail("Native tool result mismatch"))
	}
	view["status"] = "compatible"
	return finish(nil)
}
