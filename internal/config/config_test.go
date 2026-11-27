// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package config

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestDefaultsAndBounds(t *testing.T) {
	c := Defaults()
	c.Model = "test"
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if c.MaxFileBytes != 131072 || c.ModelContextTokens != 32768 || c.ToolOutputBytes != 32768 || c.ResolvedAPIKey(func(string) (string, bool) { return "token", true }) != "token" {
		t.Fatal("default drift")
	}
	changes := []struct {
		name   string
		change func(*Config)
	}{{"model", func(c *Config) { c.Model = " " }}, {"provider", func(c *Config) { c.Provider = "command" }}, {"timeout-min", func(c *Config) { c.Timeout = .099 }}, {"timeout-max", func(c *Config) { c.Timeout = 3601 }}, {"confidence", func(c *Config) { c.MinConfidence = 1.01 }}, {"nan", func(c *Config) { c.Timeout = math.NaN() }}, {"infinity", func(c *Config) { c.MinConfidence = math.Inf(1) }}, {"files", func(c *Config) { c.MaxFileBytes = 0 }}, {"rounds", func(c *Config) { c.MaxRounds = 10001 }}, {"attempts", func(c *Config) { c.ValidationAttempts = 0 }}, {"context-min", func(c *Config) { c.ModelContextTokens = 4095 }}, {"output-max", func(c *Config) { c.ModelMaxOutputTokens = 128001 }}, {"output-reservation", func(c *Config) { c.ModelMaxOutputTokens = c.ModelContextTokens }}, {"turns", func(c *Config) { c.AgentMaxTurns = 0 }}, {"tools", func(c *Config) { c.AgentMaxToolCalls = 10001 }}, {"tool-bytes", func(c *Config) { c.ToolOutputBytes = 1023 }}, {"nil-options", func(c *Config) { c.RequestOptions = nil }}, {"nonfinite-options", func(c *Config) { c.RequestOptions = map[string]any{"temperature": math.NaN()} }}, {"unsupported-options", func(c *Config) { c.RequestOptions = map[string]any{"value": func() {}} }}}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			c := Defaults()
			c.Model = "test"
			test.change(&c)
			if c.Validate() == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, key := range []string{"model", "messages", "stream", "format", "response_format", "tools", "tool_choice"} {
		t.Run("reserved-"+key, func(t *testing.T) {
			c := Defaults()
			c.Model = "test"
			c.RequestOptions = map[string]any{key: true}
			if c.Validate() == nil {
				t.Fatal("reserved option accepted")
			}
		})
	}
}
func TestEndpointPolicy(t *testing.T) {
	for _, tc := range []struct {
		url     string
		allowed bool
	}{{"https://internal.example/v1", true}, {"http://localhost:11434/api/chat", true}, {"http://127.0.0.1/v1", true}, {"http://[::1]/api/chat", true}, {"http://internal.example/v1", false}, {"https://u:p@internal.example/v1", false}, {"https://internal.example/v1?key=value", false}, {"https://internal.example/v1#fragment", false}, {"file:///tmp/model", false}} {
		t.Run(tc.url, func(t *testing.T) {
			c := Defaults()
			c.Model = "test"
			c.Endpoint = tc.url
			if (c.Validate() == nil) != tc.allowed {
				t.Fatal("endpoint policy mismatch")
			}
		})
	}
	c := Defaults()
	c.Model = "test"
	c.Endpoint = "http://internal.example/v1"
	c.AllowInsecureHTTP = true
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestCredentialPrecedenceAndIndependentRedaction(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, env := range []bool{false, true} {
			c := Defaults()
			c.Model = "test"
			if direct {
				k := "git-key"
				c.APIKey = &k
			}
			lookup := func(string) (string, bool) {
				if env {
					return "env-key", true
				}
				return "", false
			}
			want := ""
			if env {
				want = "env-key"
			}
			if direct {
				want = "git-key"
			}
			if c.ResolvedAPIKey(lookup) != want {
				t.Fatal("token precedence changed")
			}
			c.RequestOptions = map[string]any{"nested": map[string]any{"v": "original"}}
			copy := c.Redacted()
			copy.RequestOptions["nested"].(map[string]any)["v"] = "changed"
			if c.RequestOptions["nested"].(map[string]any)["v"] != "original" {
				t.Fatal("snapshot shares mutable state")
			}
			b, e := json.Marshal(copy)
			if e != nil || strings.Contains(string(b), "git-key") || strings.Contains(string(b), "env-key") {
				t.Fatal("credential archived")
			}
		}
	}
}
func TestNativeGitSettingsAndCABundle(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Write(t, g, "ca.pem", "path validation only")
	testutil.Must(t, g, "config", "ez.model", "generic-model")
	for key, value := range map[string]string{"endpoint": "http://internal.example/v1", "allowInsecureHttp": "yes", "caBundle": "ca.pem", "timeout": "12.5", "minConfidence": "0.9", "maxFileBytes": "64k", "maxRounds": "500", "validationAttempts": "4", "modelContextTokens": "65536", "modelMaxOutputTokens": "4096", "agentMaxTurns": "41", "agentMaxToolCalls": "200", "toolOutputBytes": "64k", "requestOptions": `{"integer":9007199254740993,"chat_template_kwargs":{"enable_thinking":false}}`} {
		testutil.Must(t, g, "config", "ez."+key, value)
	}
	dir := filepath.Join(g.Dir, "src")
	_ = os.Mkdir(dir, 0700)
	sub := git.Client{Dir: dir, Env: g.Env}
	c, e := Load(context.Background(), sub)
	if e != nil {
		t.Fatal(e)
	}
	if c.CABundle == nil || *c.CABundle != filepath.Join(g.Dir, "ca.pem") || c.Timeout != 12.5 || c.MaxFileBytes != 65536 || !c.AllowInsecureHTTP {
		t.Fatalf("native parsing %+v", c)
	}
	b, e := json.Marshal(c.Redacted())
	if e != nil || !strings.Contains(string(b), "9007199254740993") {
		t.Fatal("large JSON integer was rounded")
	}
}
func TestGitScopesIncludesAndWorktree(t *testing.T) {
	g := testutil.Repo(t)
	global := filepath.Join(t.TempDir(), "global.gitconfig")
	testutil.Must(t, g, "config", "--file", global, "ez.model", "global")
	testutil.Must(t, g, "config", "--file", global, "ez.endpoint", "https://models.example/v1")
	g.Env = testutil.SetEnv(g.Env, "GIT_CONFIG_GLOBAL", global)
	c, e := Load(context.Background(), g)
	if e != nil || c.Model != "global" {
		t.Fatal(c, e)
	}
	testutil.Must(t, g, "config", "ez.model", "local")
	c, e = Load(context.Background(), g)
	if e != nil || c.Model != "local" || c.Endpoint != "https://models.example/v1" {
		t.Fatal(c, e)
	}
	included := filepath.Join(t.TempDir(), "include.gitconfig")
	testutil.Must(t, g, "config", "--file", included, "ez.MODEL", "included")
	testutil.Must(t, g, "config", "include.path", included)
	c, e = Load(context.Background(), g)
	if e != nil || c.Model != "included" {
		t.Fatal(c, e)
	}
	testutil.Must(t, g, "config", "extensions.worktreeConfig", "true")
	testutil.Must(t, g, "config", "--worktree", "ez.model", "worktree")
	c, e = Load(context.Background(), g)
	if e != nil || c.Model != "worktree" {
		t.Fatal(c, e)
	}
	g.Env = testutil.SetEnv(g.Env, "GIT_CONFIG_COUNT", "1")
	g.Env = testutil.SetEnv(g.Env, "GIT_CONFIG_KEY_0", "ez.model")
	g.Env = testutil.SetEnv(g.Env, "GIT_CONFIG_VALUE_0", "command-line")
	c, e = Load(context.Background(), g)
	if e != nil || c.Model != "command-line" {
		t.Fatal(c, e)
	}
	// Provider options replace the complete object at a higher scope.
	testutil.Must(t, g, "config", "--file", global, "ez.requestOptions", `{"a":1,"b":2}`)
	testutil.Must(t, g, "config", "ez.requestOptions", `{"a":3}`)
	c, e = Load(context.Background(), g)
	if e != nil || len(c.RequestOptions) != 1 {
		t.Fatal(c, e)
	}
}
func TestOllamaDefaultsAndExplicitOverrides(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.provider", "ollama")
	testutil.Must(t, g, "config", "ez.model", "local:tag")
	c, e := Load(context.Background(), g)
	if e != nil || c.Endpoint != "http://localhost:11434/api/chat" || c.APIKeyEnv != "LOCAL_MODEL_API_KEY" {
		t.Fatal(c, e)
	}
	testutil.Must(t, g, "config", "ez.endpoint", "https://internal.example/api/chat")
	testutil.Must(t, g, "config", "ez.apiKeyEnv", "EXPLICIT_TOKEN")
	c, e = Load(context.Background(), g)
	if e != nil || c.APIKeyEnv != "EXPLICIT_TOKEN" || c.Endpoint != "https://internal.example/api/chat" {
		t.Fatal(c, e)
	}
}
func TestInvalidSettingsNeverEchoValues(t *testing.T) {
	for _, test := range []struct{ key, value string }{{"unknown", "sensitive-value"}, {"config", "unused"}, {"resolver", "unused"}, {"command", `["python", 1]`}, {"apiKey", ""}, {"caBundle", ""}, {"caBundle", "/nonexistent/git-ez-ca.pem"}, {"allowInsecureHttp", "sometimes"}, {"maxRounds", "1.5"}, {"timeout", "forever"}, {"requestOptions", "not-json"}, {"requestOptions", "[]"}, {"requestOptions", `{"value":1e999}`}, {"requestOptions", `{"model":"secret-value"}`}, {"apiKey", " "}} {
		t.Run(test.key+test.value, func(t *testing.T) {
			g := testutil.Repo(t)
			testutil.Must(t, g, "config", "ez.model", "test")
			testutil.Must(t, g, "config", "ez."+test.key, test.value)
			_, e := Load(context.Background(), g)
			if e == nil || (strings.TrimSpace(test.value) != "" && strings.Contains(e.Error(), test.value)) {
				t.Fatalf("invalid setting exposed: %v", e)
			}
		})
	}
	g := testutil.Repo(t)
	testutil.Write(t, g, ".git/config", "[private-token-never-echo\n")
	_, e := Load(context.Background(), g)
	if e == nil || strings.Contains(e.Error(), "private-token") {
		t.Fatal("malformed config exposed")
	}
}
func TestOutsideRepositoryAndNoImplicitDotenv(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.model", "test")
	testutil.Must(t, g, "config", "ez.apiKeyEnv", "EZ_NO_DOTENV")
	testutil.Write(t, g, ".env", "EZ_NO_DOTENV=project-secret\n")
	c, e := Load(context.Background(), g)
	if e != nil || c.ResolvedAPIKey(func(string) (string, bool) { return "", false }) != "" {
		t.Fatal(c, e)
	}
	global := filepath.Join(t.TempDir(), "settings")
	testutil.Must(t, g, "config", "--file", global, "ez.model", "outside")
	outside := git.Client{Dir: t.TempDir(), Env: testutil.SetEnv(g.Env, "GIT_CONFIG_GLOBAL", global)}
	c, e = Load(context.Background(), outside)
	if e != nil || c.Model != "outside" {
		t.Fatal(c, e)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	path, e := expandHome(context.Background(), g, "~/ca.pem")
	if e != nil || path != filepath.Join(home, "ca.pem") {
		t.Fatal(path, e)
	}
}

func TestStandardGitExampleCanBeIncluded(t *testing.T) {
	g := testutil.Repo(t)
	example, e := filepath.Abs("../../examples/ez.gitconfig")
	if e != nil {
		t.Fatal(e)
	}
	testutil.Must(t, g, "config", "include.path", example)
	c, e := Load(context.Background(), g)
	if e != nil || c.Model != "exact-model-id-on-your-server" || c.MaxFileBytes != 262144 {
		t.Fatal(c, e)
	}
}

func TestNativeHomeCAAndLiteralNonGitPathInterpolation(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.model", "test")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(home, "ca.pem")
	if e := os.WriteFile(ca, []byte("fixture CA path"), 0600); e != nil {
		t.Fatal(e)
	}
	g.Env = testutil.SetEnv(g.Env, "HOME", home)
	testutil.Must(t, g, "config", "ez.caBundle", "~/ca.pem")
	c, e := Load(context.Background(), g)
	if e != nil || c.CABundle == nil || *c.CABundle != ca {
		t.Fatal(c, e)
	}
	testutil.Write(t, g, "%(prefix)/ca.pem", "literal path")
	testutil.Must(t, g, "config", "ez.caBundle", "%(prefix)/ca.pem")
	c, e = Load(context.Background(), g)
	if e != nil || *c.CABundle != filepath.Join(g.Dir, "%(prefix)/ca.pem") {
		t.Fatal("new Git path expansion introduced", c, e)
	}
}
func TestEveryIntegerBudgetBoundary(t *testing.T) {
	fields := []struct {
		name     string
		field    func(*Config) *int
		min, max int
	}{
		{"max_file_bytes", func(c *Config) *int { return &c.MaxFileBytes }, 1, 10_000_000},
		{"max_rounds", func(c *Config) *int { return &c.MaxRounds }, 1, 10000},
		{"validation_attempts", func(c *Config) *int { return &c.ValidationAttempts }, 1, 1000},
		{"model_context_tokens", func(c *Config) *int { return &c.ModelContextTokens }, 4096, 2_000_000},
		{"model_max_output_tokens", func(c *Config) *int { return &c.ModelMaxOutputTokens }, 1, 128000},
		{"agent_max_turns", func(c *Config) *int { return &c.AgentMaxTurns }, 1, 1000},
		{"agent_max_tool_calls", func(c *Config) *int { return &c.AgentMaxToolCalls }, 1, 10000},
		{"tool_output_bytes", func(c *Config) *int { return &c.ToolOutputBytes }, 1024, 1_000_000},
	}
	for _, field := range fields {
		for _, value := range []int{field.min - 1, field.min, field.max, field.max + 1} {
			t.Run(field.name+"/"+strconv.Itoa(value), func(t *testing.T) {
				c := Defaults()
				c.Model = "test"
				c.ModelContextTokens = 2_000_000
				c.ModelMaxOutputTokens = 1
				*field.field(&c) = value
				valid := value >= field.min && value <= field.max
				if (c.Validate() == nil) != valid {
					t.Fatal("budget boundary", value)
				}
			})
		}
	}
}

func TestRemovedContextByteSettingDoesNotRestrictResolution(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Must(t, g, "config", "ez.model", "fixture")
	testutil.Must(t, g, "config", "ez.agentMaxContextBytes", "obsolete-invalid-value")
	c, err := Load(context.Background(), g)
	if err != nil || c.Model != "fixture" {
		t.Fatal(c, err)
	}
	raw, err := json.Marshal(c.Redacted())
	if err != nil || strings.Contains(strings.ToLower(string(raw)), "agentmaxcontextbytes") {
		t.Fatal(string(raw), err)
	}
}

func TestCABundleMissingComponentParentPreservesReferenceResolution(t *testing.T) {
	g := testutil.Repo(t)
	physical := t.TempDir()
	if err := os.Mkdir(physical+"/child", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical+"/child", g.Dir+"/link"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(physical+"/ca.pem", []byte("configured file"), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Must(t, g, "config", "ez.model", "fixture")
	for _, path := range []string{"link/../missing/../ca.pem", "missing/../link/../ca.pem"} {
		testutil.Must(t, g, "config", "ez.caBundle", path)
		c, err := Load(context.Background(), g)
		want, _ := filepath.EvalSymlinks(physical + "/ca.pem")
		if err != nil || c.CABundle == nil || *c.CABundle != want {
			t.Fatal(c.CABundle, err, want)
		}
	}
}
