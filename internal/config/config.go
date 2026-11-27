// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package config reads only native ez.* Git configuration, with Git's own
// precedence, includes, booleans and integer syntax.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/number"
)

type Config struct {
	Provider             string         `json:"provider"`
	Model                string         `json:"model"`
	Endpoint             string         `json:"endpoint"`
	APIKey               *string        `json:"api_key"`
	APIKeyEnv            string         `json:"api_key_env"`
	AllowInsecureHTTP    bool           `json:"allow_insecure_http"`
	CABundle             *string        `json:"ca_bundle"`
	Timeout              float64        `json:"timeout"`
	MinConfidence        float64        `json:"min_confidence"`
	MaxFileBytes         int            `json:"max_file_bytes"`
	ValidationAttempts   int            `json:"validation_attempts"`
	MaxRounds            int            `json:"max_rounds"`
	ModelContextTokens   int            `json:"model_context_tokens"`
	ModelMaxOutputTokens int            `json:"model_max_output_tokens"`
	AgentMaxTurns        int            `json:"agent_max_turns"`
	AgentMaxToolCalls    int            `json:"agent_max_tool_calls"`
	ToolOutputBytes      int            `json:"tool_output_bytes"`
	RequestOptions       map[string]any `json:"request_options"`
}

func Defaults() Config {
	return Config{Provider: "openai", Endpoint: "https://api.openai.com/v1/chat/completions", APIKeyEnv: "OPENAI_API_KEY", Timeout: 120, MinConfidence: .8, MaxFileBytes: 131072, ValidationAttempts: 3, MaxRounds: 100, ModelContextTokens: 32768, ModelMaxOutputTokens: 8192, AgentMaxTurns: 40, AgentMaxToolCalls: 100, ToolOutputBytes: 32768, RequestOptions: map[string]any{}}
}
func (c Config) TimeoutDuration() time.Duration {
	return time.Duration(c.Timeout * float64(time.Second))
}
func (c Config) ResolvedAPIKey(lookup func(string) (string, bool)) string {
	if c.APIKey != nil && *c.APIKey != "" {
		return *c.APIKey
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, _ := lookup(c.APIKeyEnv)
	return value
}
func (c Config) Redacted() Config {
	if c.APIKey != nil {
		x := "<redacted>"
		c.APIKey = &x
	}
	if c.CABundle != nil {
		x := *c.CABundle
		c.CABundle = &x
	}
	// Snapshots must not share mutable provider option maps with their caller.
	if c.RequestOptions != nil {
		raw, e := json.Marshal(c.RequestOptions)
		if e == nil {
			var copy map[string]any
			if copyOptions, err := decodeOptions(raw); err == nil {
				copy = copyOptions
				c.RequestOptions = copy
			}
		}
	}
	return c
}
func (c Config) Validate() error {
	invalid := func(message string) error { return failure.New(failure.Configuration, message) }
	if c.Provider != "openai" && c.Provider != "ollama" {
		return invalid("provider must be openai or ollama")
	}
	if strings.TrimSpace(c.Model) == "" {
		return invalid("model is required")
	}
	if c.APIKey != nil && strings.TrimSpace(*c.APIKey) == "" {
		return invalid("api_key must be a non-empty string")
	}
	if !endpointAllowed(c.Endpoint, c.AllowInsecureHTTP) {
		return invalid("endpoint must use HTTPS; for a trusted HTTP server set allow_insecure_http=true explicitly")
	}
	if c.CABundle != nil {
		if strings.TrimSpace(*c.CABundle) == "" {
			return invalid("ca_bundle must be a non-empty path to a CA bundle file")
		}
		info, e := os.Stat(*c.CABundle)
		if e != nil || !info.Mode().IsRegular() {
			return invalid("ca_bundle file not found")
		}
	}
	for _, r := range []struct {
		name        string
		v, min, max float64
	}{{"timeout", c.Timeout, .1, 3600}, {"min_confidence", c.MinConfidence, 0, 1}, {"max_file_bytes", float64(c.MaxFileBytes), 1, 10_000_000}, {"max_rounds", float64(c.MaxRounds), 1, 10000}, {"validation_attempts", float64(c.ValidationAttempts), 1, 1000}, {"model_context_tokens", float64(c.ModelContextTokens), 4096, 2_000_000}, {"model_max_output_tokens", float64(c.ModelMaxOutputTokens), 1, 128000}, {"agent_max_turns", float64(c.AgentMaxTurns), 1, 1000}, {"agent_max_tool_calls", float64(c.AgentMaxToolCalls), 1, 10000}, {"tool_output_bytes", float64(c.ToolOutputBytes), 1024, 1_000_000}} {
		if math.IsNaN(r.v) || math.IsInf(r.v, 0) || r.v < r.min || r.v > r.max {
			return invalid(fmt.Sprintf("%s must be between %g and %g", r.name, r.min, r.max))
		}
	}
	if c.ModelMaxOutputTokens >= c.ModelContextTokens {
		return invalid("model_max_output_tokens must be smaller than model_context_tokens")
	}
	if c.RequestOptions == nil {
		return invalid("request_options must be an object")
	}
	for _, key := range []string{"model", "messages", "stream", "format", "response_format", "tools", "tool_choice"} {
		if _, ok := c.RequestOptions[key]; ok {
			return invalid("request_options cannot override model, messages or output format")
		}
	}
	if !finiteNumbers(c.RequestOptions) {
		return invalid("request_options must contain finite JSON-compatible values")
	}
	if _, e := json.Marshal(c.RequestOptions); e != nil {
		return invalid("request_options must contain finite JSON-compatible values")
	}
	return nil
}

func Load(ctx context.Context, g git.Client) (Config, error) {
	c := Defaults()
	r, e := g.Run(ctx, "config", "--null", "--get-regexp", `^ez\.`)
	if e != nil {
		return c, e
	}
	if r.ExitCode != 0 && r.ExitCode != 1 {
		return c, failure.New(failure.Configuration, "Cannot read ez.* Git configuration")
	}
	if !utf8.Valid(r.Stdout) {
		return c, failure.New(failure.Configuration, "Cannot decode ez.* Git configuration")
	}
	values := map[string]string{}
	for _, entry := range strings.Split(string(r.Stdout), "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		values[strings.ToLower(key)] = value
	}
	// Retired context-byte configuration has no effect; existing Git settings
	// must not reintroduce an admission policy or prevent migration.
	delete(values, "ez.agentmaxcontextbytes")
	setters := map[string]func(string){
		"ez.provider": func(v string) { c.Provider = v }, "ez.model": func(v string) { c.Model = v }, "ez.endpoint": func(v string) { c.Endpoint = v }, "ez.apikey": func(v string) { c.APIKey = &v }, "ez.apikeyenv": func(v string) { c.APIKeyEnv = v }, "ez.cabundle": func(v string) { c.CABundle = &v },
	}
	ints := map[string]*int{"ez.maxfilebytes": &c.MaxFileBytes, "ez.maxrounds": &c.MaxRounds, "ez.validationattempts": &c.ValidationAttempts, "ez.modelcontexttokens": &c.ModelContextTokens, "ez.modelmaxoutputtokens": &c.ModelMaxOutputTokens, "ez.agentmaxturns": &c.AgentMaxTurns, "ez.agentmaxtoolcalls": &c.AgentMaxToolCalls, "ez.tooloutputbytes": &c.ToolOutputBytes}
	floats := map[string]*float64{"ez.timeout": &c.Timeout, "ez.minconfidence": &c.MinConfidence}
	var unknown []string
	for key := range values {
		_, s := setters[key]
		_, i := ints[key]
		_, f := floats[key]
		if !s && !i && !f && key != "ez.allowinsecurehttp" && key != "ez.requestoptions" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return c, failure.New(failure.Configuration, "Unknown Git configuration keys: "+strings.Join(unknown, ", "))
	}
	bad := func(key, kind string) error {
		return failure.New(failure.Configuration, "Invalid Git configuration: "+key+" must be "+kind+". Configure ez.model and provider settings with git config.")
	}
	// Reading is explicit and deterministic. Git still interprets numeric suffixes
	// and booleans; Go never attempts to approximate Git's config grammar.
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := values[key]
		if set, ok := setters[key]; ok {
			set(v)
			continue
		}
		if target, ok := ints[key]; ok {
			p, err := g.Run(ctx, "config", "--type=int", "--get", key)
			if err != nil {
				return c, err
			}
			if p.ExitCode != 0 {
				return c, bad(key, "a Git integer")
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(p.Stdout)))
			if err != nil {
				return c, bad(key, "a Git integer")
			}
			*target = n
			continue
		}
		if target, ok := floats[key]; ok {
			n, err := number.Float(v)
			if err != nil {
				return c, bad(key, "a finite number")
			}
			*target = n
			continue
		}
		switch key {
		case "ez.allowinsecurehttp":
			p, err := g.Run(ctx, "config", "--type=bool", "--get", key)
			if err != nil {
				return c, err
			}
			if p.ExitCode != 0 {
				return c, bad(key, "a Git boolean")
			}
			c.AllowInsecureHTTP = strings.TrimSpace(string(p.Stdout)) == "true"
		case "ez.requestoptions":
			var options map[string]any
			var optionErr error
			options, optionErr = decodeOptions([]byte(v))
			if optionErr != nil || options == nil {
				return c, bad(key, "a finite JSON object")
			}
			c.RequestOptions = options
		}
	}
	if c.Provider == "ollama" {
		if _, ok := values["ez.endpoint"]; !ok {
			c.Endpoint = "http://localhost:11434/api/chat"
		}
		if _, ok := values["ez.apikeyenv"]; !ok {
			c.APIKeyEnv = "LOCAL_MODEL_API_KEY"
		}
	}
	if c.CABundle != nil && strings.TrimSpace(*c.CABundle) != "" {
		expanded, err := expandHome(ctx, g, *c.CABundle)
		if err != nil {
			return c, failure.Wrap(failure.Configuration, "Cannot expand CA bundle path", err)
		}
		base := g.Dir
		p, err := g.Run(ctx, "rev-parse", "--show-toplevel")
		if err != nil {
			return c, err
		}
		if p.ExitCode == 0 {
			base = strings.TrimSpace(string(p.Stdout))
		}
		if !filepath.IsAbs(expanded) {
			// Preserve symlink/.. for physical resolution before lexical cleaning.
			expanded = base + string(os.PathSeparator) + expanded
		}
		resolved, err := resolvePath(expanded)
		if err != nil {
			return c, failure.Wrap(failure.Configuration, "Cannot resolve CA bundle path", err)
		}
		c.CABundle = &resolved
	}
	if err := c.Validate(); err != nil {
		return c, failure.Wrap(failure.Configuration, "Invalid Git configuration: "+err.Error()+". Configure ez.model and provider settings with git config.", err)
	}
	return c, nil
}

// Git performs native account/home lookup, including macOS Directory Services
// accounts unavailable to Go's CGO-disabled os/user implementation. Only tilde
// paths take this route: Git-specific %(prefix) interpolation is not introduced.
func expandHome(ctx context.Context, g git.Client, p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	r, e := g.Run(ctx, "-c", "ez.caBundle="+p, "config", "--path", "--get", "ez.caBundle")
	if e != nil {
		return "", e
	}
	if r.ExitCode != 0 {
		return "", failure.New(failure.Configuration, "Cannot expand CA bundle home directory")
	}
	return strings.TrimSuffix(string(r.Stdout), "\n"), nil
}

// Match Python Path.resolve(strict=False), resolving existing components before
// simplifying parents. A missing suffix is retained for normal file validation.
func resolvePath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = cwd + string(os.PathSeparator) + p
	}
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
				physical, e := filepath.EvalSymlinks(resolved)
				if e == nil {
					resolved = physical
				} else if !os.IsNotExist(e) {
					return "", e
				}
			}
			return filepath.Abs(resolved)
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// filepath.Dir would clean symlink/.. before resolving the prefix.
		p = strings.TrimRight(p, string(os.PathSeparator))
		i := strings.LastIndexByte(p, os.PathSeparator)
		if i < 0 {
			return "", err
		}
		tail = append(tail, p[i+1:])
		p = p[:i]
		if p == "" {
			p = string(os.PathSeparator)
		}
	}
}

func decodeOptions(raw []byte) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var values map[string]any
	if e := d.Decode(&values); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("trailing request option data")
	}
	if !finiteNumbers(values) {
		return nil, fmt.Errorf("nonfinite request option")
	}
	return values, nil
}
func finiteNumbers(v any) bool {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			n, e := strconv.ParseFloat(x.String(), 64)
			return e == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
	case []any:
		for _, item := range x {
			if !finiteNumbers(item) {
				return false
			}
		}
	case map[string]any:
		for _, item := range x {
			if !finiteNumbers(item) {
				return false
			}
		}
	}
	return true
}

// endpointAllowed implements the reference admission policy, not an HTTP URL
// validator. Python urlsplit does not validate ports or percent-encoded paths;
// the provider must still reject unusable endpoints at connection time. Preserve
// that boundary even for a clean integration that never calls a provider.
func endpointAllowed(raw string, allowHTTP bool) bool {
	for len(raw) > 0 && raw[0] <= 0x20 {
		raw = raw[1:]
	}
	raw = strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(raw)
	scheme, rest, ok := strings.Cut(raw, ":")
	scheme = strings.ToLower(scheme)
	if !ok || (scheme != "http" && scheme != "https") || !strings.HasPrefix(rest, "//") {
		return false
	}
	rest = strings.TrimPrefix(rest, "//")
	beforeFragment, fragment, hasFragment := strings.Cut(rest, "#")
	if hasFragment && fragment != "" {
		return false
	}
	beforeQuery, query, hasQuery := strings.Cut(beforeFragment, "?")
	if hasQuery && query != "" {
		return false
	}
	authority, _, _ := strings.Cut(beforeQuery, "/")
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		credentials := authority[:i]
		if credentials != "" && credentials != ":" {
			return false
		}
		authority = authority[i+1:]
	}
	host := ""
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return false
		}
		host = authority[1:end]
		if !regexp.MustCompile(`(?i)^v[0-9a-f]+\..+$`).MatchString(host) {
			addr, e := netip.ParseAddr(host)
			if e != nil || !addr.Is6() {
				return false
			}
		}
	} else {
		if strings.ContainsAny(authority, "[]") {
			return false
		}
		host, _, _ = strings.Cut(authority, ":")
	}
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	return scheme == "https" || local || allowHTTP
}
