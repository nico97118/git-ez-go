// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"github.com/nico97118/git-ez-go/internal/config"
	"strings"
	"testing"
)

func TestChildEnvironmentPreservesGitScopesWithoutCredentials(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "ez.apiKey")
	t.Setenv("GIT_CONFIG_VALUE_0", "key'with spaces")
	t.Setenv("GIT_CONFIG_KEY_1", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_1", "Quoted User")
	t.Setenv("GIT_CONFIG_PARAMETERS", `'ez.apiKey'='key'\''with spaces' 'user.email=valid@example.invalid'`)
	t.Setenv("SELECTED_TOKEN", "transported-secret")
	c := config.Defaults()
	c.APIKeyEnv = "SELECTED_TOKEN"
	s := session{config: c}
	env, e := s.shellEnvironment()
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"key'with spaces", "transported-secret", "SELECTED_TOKEN="} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("credential inherited")
		}
	}
	if !strings.Contains(joined, "GIT_CONFIG_VALUE_1=Quoted User") || !strings.Contains(joined, "user.email=valid@example.invalid") {
		t.Fatal("Git scopes discarded")
	}
}
func TestNativeReferenceOllamaOptionsAndMetadata(t *testing.T) {
	c := config.Defaults()
	c.RequestOptions = map[string]any{"options": map[string]any{"num_predict": "512", "temperature": "0.2"}, "top_logprobs": 2, "reasoning": "low", "name": "local name", "tags": []any{"tag"}, "metadata": map[string]any{"label": "test"}, "disable_streaming": "tool_calling", "verbose": false, "cache": false, "custom_get_token_ids": nil, "rate_limiter": nil, "output_version": "v1"}
	s := session{config: c}
	wire, e := nativeOptions(&s)
	if e != nil {
		t.Fatal(e)
	}
	opts := wire["options"].(map[string]any)
	if opts["num_predict"] != 512 || opts["temperature"] != .2 || wire["think"] != "low" || wire["logprobs"] != true {
		t.Fatal(wire)
	}
	if _, exists := wire["metadata"]; exists {
		t.Fatal("client metadata leaked to provider")
	}
	c.RequestOptions["logprobs"] = false
	s.config = c
	if _, e = nativeOptions(&s); e == nil {
		t.Fatal("inconsistent logprobs accepted")
	}
}
