// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/ai"
	aix "github.com/firebase/genkit/go/ai/exp"
	"github.com/firebase/genkit/go/core/tracing"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/compat_oai"
	"github.com/firebase/genkit/go/plugins/ollama"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var localInstrumentation sync.Once

func baseURL(endpoint, provider string) string {
	suffix := "/chat/completions"
	if provider == "ollama" {
		suffix = "/api/chat"
	}
	return strings.TrimSuffix(strings.TrimRight(endpoint, "/"), suffix)
}

type chatConfig struct {
	compat_oai.RequestConfig
	MaxCompletionTokens int64 `json:"maxCompletionTokens"`
}

func (c chatConfig) ApplyToChatCompletion(p *openai.ChatCompletionNewParams) {
	p.MaxCompletionTokens = openai.Int(c.MaxCompletionTokens)
}

type providerTransport struct {
	base   *http.Transport
	s      *session
	target *url.URL
	native map[string]any
}

func (s *session) model(ctx context.Context) (*genkit.Genkit, ai.ModelRef, error) {
	if e := safeInitEnvironment(); e != nil {
		return nil, ai.ModelRef{}, e
	}
	// Replace Genkit's implicit process-global OTel provider. Context-local
	// observers remain active. No exporter/development plugin is registered.
	localInstrumentation.Do(func() { tracing.SetInstrumentation(aix.Observer{}) })
	base := baseURL(s.config.Endpoint, s.config.Provider)
	if strings.HasSuffix(base, "/responses") {
		return nil, ai.ModelRef{}, fail("Chat Completions is required; Responses endpoints are unsupported")
	}
	suffix := "/chat/completions"
	if s.config.Provider == "ollama" {
		suffix = "/api/chat"
	}
	target, e := url.Parse(base + suffix)
	if e != nil || target.Host == "" {
		return nil, ai.ModelRef{}, fail("Invalid provider endpoint")
	}
	var roots *x509.CertPool
	if s.config.CABundle != nil {
		roots = x509.NewCertPool()
		b, e := os.ReadFile(*s.config.CABundle)
		if e != nil || !roots.AppendCertsFromPEM(b) {
			return nil, ai.ModelRef{}, fail("Cannot load custom CA bundle")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	s.closeTransport = transport.CloseIdleConnections
	tr := &providerTransport{base: transport, s: s, target: target}
	s.transport = tr
	client := &http.Client{Transport: tr, Timeout: s.config.TimeoutDuration(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if s.config.Provider == "openai" {
		plugin := &compat_oai.OpenAICompatible{Provider: "git-ez", APIKey: s.authorizationKey(), BaseURL: base, Opts: []option.RequestOption{option.WithHTTPClient(client), option.WithMaxRetries(0)}}
		g := genkit.Init(ctx, genkit.WithPlugins(plugin), genkit.WithExperimental(), genkit.WithPromptFS(promptFS), genkit.WithPromptDir("."))
		action := compat_oai.NewChatModel[chatConfig](plugin, s.config.Model, ai.ModelOptions{Supports: &ai.ModelSupports{Tools: true, Multiturn: true, SystemRole: true, Output: []string{"text"}}})
		genkit.RegisterAction(g, action)
		return g, ai.NewModelRef("git-ez/"+s.config.Model, chatConfig{RequestConfig: compat_oai.RequestConfig{Extra: s.config.RequestOptions}, MaxCompletionTokens: int64(s.config.ModelMaxOutputTokens)}), nil
	}
	tr.native, e = nativeOptions(s)
	if e != nil {
		return nil, ai.ModelRef{}, e
	}
	plugin := &ollama.Ollama{ServerAddress: base, Timeout: int(s.config.Timeout) + 1, HTTPClient: client, Headers: http.Header{"Authorization": []string{""}}}
	g := genkit.Init(ctx, genkit.WithPlugins(plugin), genkit.WithExperimental(), genkit.WithPromptFS(promptFS), genkit.WithPromptDir("."))
	plugin.DefineModel(g, ollama.ModelDefinition{Name: s.config.Model, Type: "chat"}, &ai.ModelOptions{Supports: &ai.ModelSupports{Tools: true, Multiturn: true, SystemRole: true, Output: []string{"text"}}})
	cfg := ollama.GenerateContentConfig{NumCtx: &s.config.ModelContextTokens, NumPredict: &s.config.ModelMaxOutputTokens}
	return g, ai.NewModelRef("ollama/"+s.config.Model, cfg), nil
}
func number(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), float64(int(n)) == n
	case json.Number:
		x, e := n.Int64()
		return int(x), e == nil
	}
	return 0, false
}
func (t *providerTransport) RoundTrip(original *http.Request) (*http.Response, error) {
	s := t.s
	r := original.Clone(original.Context())
	if r.URL.Scheme != t.target.Scheme || r.URL.Host != t.target.Host {
		return nil, s.abort(fail("Provider attempted a different endpoint"))
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var request map[string]any
	if e := decoder.Decode(&request); e != nil {
		return nil, s.abort(fail("Provider request encoding failed"))
	}
	messages, ok := request["messages"].([]any)
	if !ok {
		return nil, fail("Provider messages are required")
	}
	if s.config.Provider == "ollama" {
		if e := restoreOllamaThinking(r.Context(), messages); e != nil {
			return nil, s.abort(e)
		}
		var e error
		messages, e = restoreOllamaTools(r.Context(), messages)
		if e != nil {
			return nil, s.abort(e)
		}
	}
	if s.permanent != "" {
		anchored := false
		if len(messages) > 0 {
			first, _ := messages[0].(map[string]any)
			anchored = first["role"] == "system" && first["content"] == s.permanent
		}
		if !anchored {
			messages = append([]any{map[string]any{"role": "system", "content": s.permanent}}, messages...)
		}
	}
	request["messages"] = messages
	if t.native != nil {
		for k, v := range t.native {
			request[k] = v
		}
	}
	raw, e := json.Marshal(request)
	if e != nil {
		return nil, fail("Cannot encode provider request")
	}
	s.mu.Lock()
	if s.calls >= s.config.AgentMaxTurns {
		s.mu.Unlock()
		return nil, s.abort(fail("Actual model request limit exceeded"))
	}
	s.calls++
	index := s.calls
	s.mu.Unlock()
	if e = s.record("model_request", map[string]any{"number": index, "provider": s.config.Provider, "request": request, "input_bytes": len(raw)}); e != nil {
		return nil, e
	}
	r.Header.Del("Authorization")
	if key := s.authorizationKey(); key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	r.URL = t.target
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	started := time.Now()
	response, e := t.base.RoundTrip(r)
	if e != nil {
		_ = s.record("model_http_failure", map[string]any{"number": index, "duration_seconds": time.Since(started).Seconds(), "error_type": fmt.Sprintf("%T", e)})
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		return nil, fail("Model HTTP transport failed")
	}
	s.lastStatus = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		_ = s.record("model_http_failure", map[string]any{"number": index, "duration_seconds": time.Since(started).Seconds(), "status_code": response.StatusCode})
		response.Body = io.NopCloser(strings.NewReader(`{"error":{"message":"Provider HTTP failure","type":"provider_error"}}`))
		response.Header = response.Header.Clone()
		response.Header.Del("Location")
		response.ContentLength = -1
		return response, nil
	}
	data, e := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if e != nil {
		_ = s.record("model_body_failure", map[string]any{"number": index, "received_bytes": len(data), "duration_seconds": time.Since(started).Seconds(), "error_type": fmt.Sprintf("%T", e)})
		if cancelled := r.Context().Err(); cancelled != nil {
			return nil, s.abort(cancelled)
		}
		return nil, s.abort(failure.Wrap(failure.Execution, "Cannot read complete model response", e))
	}
	var result any
	decode := json.NewDecoder(bytes.NewReader(data))
	decode.UseNumber()
	if e = decode.Decode(&result); e != nil {
		return nil, fail("Malformed provider response")
	}
	if e = s.record("model_http_response", map[string]any{"number": index, "duration_seconds": time.Since(started).Seconds(), "response": result}); e != nil {
		return nil, e
	}
	if object, ok := result.(map[string]any); ok {
		// Genkit synthesizes absent refs. The compatibility probe must still
		// verify the provider's actual OpenAI wire identity, as Python did.
		s.missingWireToolReference = false
		if s.config.Provider == "openai" {
			choices, _ := object["choices"].([]any)
			for _, choice := range choices {
				item, _ := choice.(map[string]any)
				message, _ := item["message"].(map[string]any)
				calls, _ := message["tool_calls"].([]any)
				for _, call := range calls {
					value, _ := call.(map[string]any)
					ref, _ := value["id"].(string)
					if ref == "" {
						s.missingWireToolReference = true
					}
				}
			}
		}
		inTokens, outTokens := 0, 0
		reported := false
		if usage, ok := object["usage"].(map[string]any); ok {
			reported = true
			inTokens, _ = number(usage["prompt_tokens"])
			outTokens, _ = number(usage["completion_tokens"])
		}
		if t.native != nil {
			_, reported = object["prompt_eval_count"]
			inTokens, _ = number(object["prompt_eval_count"])
			outTokens, _ = number(object["eval_count"])
		}
		candidates := 1
		if n, ok := number(request["n"]); ok && n > 0 {
			candidates = n
		}
		if e = s.record("resource_usage", map[string]any{"number": index, "reported": reported, "input_tokens": inTokens, "output_tokens": outTokens, "candidate_count": candidates}); e != nil {
			return nil, e
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	return response, nil
}
func nativeOptions(s *session) (map[string]any, error) {
	values := map[string]any{"num_ctx": s.config.ModelContextTokens, "num_predict": s.config.ModelMaxOutputTokens}
	if nested, ok := s.config.RequestOptions["options"]; ok {
		m, ok := nested.(map[string]any)
		if !ok {
			return nil, fail("Ollama request_options.options must be an object")
		}
		for k, v := range m {
			values[k] = v
		}
	}
	for k, v := range s.config.RequestOptions {
		if k != "options" {
			values[k] = v
		}
	}
	if v, ok := values["think"]; ok {
		values["reasoning"] = v
		delete(values, "think")
	}
	generation := map[string]bool{}
	for _, key := range strings.Fields("mirostat mirostat_eta mirostat_tau num_ctx num_gpu num_thread num_predict repeat_last_n repeat_penalty temperature seed stop tfs_z top_k top_p") {
		generation[key] = true
	}
	opts := map[string]any{}
	wire := map[string]any{}
	for key, value := range values {
		if !generation[key] && !localOption[key] && key != "reasoning" && key != "keep_alive" && key != "logprobs" && key != "top_logprobs" {
			return nil, fail("Unsupported Ollama generation option: " + key)
		}
		normalized, e := nativeValue(key, value)
		if e != nil {
			return nil, e
		}
		value = normalized
		if localOption[key] {
			if e = s.record("provider_local_option", map[string]any{"name": key, "value": value}); e != nil {
				return nil, e
			}
			continue
		}
		if value == nil {
			continue
		}
		switch {
		case generation[key]:
			opts[key] = value
		case key == "reasoning":
			if value != nil {
				switch value.(type) {
				case bool, string:
					wire["think"] = value
				default:
					return nil, fail("Invalid Ollama reasoning option")
				}
			}
		case key == "keep_alive" || key == "logprobs" || key == "top_logprobs":
			wire[key] = value
		default:
			return nil, fail("Unsupported Ollama generation option: " + key)
		}
	}
	if value, ok := wire["top_logprobs"]; ok {
		n, ok := number(value)
		if !ok || n < 1 {
			return nil, fail("top_logprobs must be positive")
		}
		if enabled, exists := wire["logprobs"]; exists && enabled == false {
			return nil, fail("top_logprobs conflicts with logprobs=false")
		}
		wire["logprobs"] = true
	}
	wire["options"] = opts
	return wire, nil
}

// The pinned OpenAI reference explicitly supplies a non-secret local sentinel
// when no selected credential exists. Ollama remains unauthenticated in that case.
func (s *session) authorizationKey() string {
	key := s.key()
	if key == "" && s.config.Provider == "openai" {
		return "local-no-key"
	}
	return key
}
