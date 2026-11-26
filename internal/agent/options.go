// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Normalize the pinned reference's JSON-expressible provider fields. LangChain
// identity/log metadata stays local; transport/client overrides remain forbidden.
func nativeValue(key string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	ints := strings.Fields("mirostat num_ctx num_gpu num_thread num_predict repeat_last_n seed top_k top_logprobs")
	floats := strings.Fields("mirostat_eta mirostat_tau repeat_penalty temperature tfs_z top_p")
	for _, k := range ints {
		if k == key {
			if n, ok := number(value); ok {
				return n, nil
			}
			if b, ok := value.(bool); ok {
				if b {
					return 1, nil
				}
				return 0, nil
			}
			if text, ok := value.(string); ok {
				n, e := strconv.Atoi(strings.TrimSpace(text))
				if e == nil {
					return n, nil
				}
			}
			return nil, fail("Invalid Ollama integer option: " + key)
		}
	}
	for _, k := range floats {
		if k == key {
			switch v := value.(type) {
			case json.Number:
				n, e := v.Float64()
				if e == nil {
					return n, nil
				}
			case float64:
				return v, nil
			case int:
				return float64(v), nil
			case bool:
				if v {
					return float64(1), nil
				}
				return float64(0), nil
			case string:
				n, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if e == nil {
					return n, nil
				}
			}
			return nil, fail("Invalid Ollama numeric option: " + key)
		}
	}
	switch key {
	case "reasoning":
		switch value.(type) {
		case bool, string:
			return value, nil
		}
		return nil, fail("Invalid Ollama reasoning option")
	case "stop", "tags":
		switch v := value.(type) {
		case []string:
			return v, nil
		case []any:
			for _, x := range v {
				if _, ok := x.(string); !ok {
					return nil, fail("Ollama string list required")
				}
			}
			return v, nil
		}
		return nil, fail("Ollama string list required")
	case "keep_alive":
		if _, ok := value.(string); ok {
			return value, nil
		}
		if n, ok := number(value); ok {
			return n, nil
		}
		return nil, fail("Invalid Ollama keep_alive option")
	case "verbose", "logprobs":
		return boolValue(value)
	case "name", "output_version":
		if _, ok := value.(string); !ok {
			return nil, fail("String metadata option required")
		}
		return value, nil
	case "metadata":
		if _, ok := value.(map[string]any); !ok {
			return nil, fail("Metadata must be an object")
		}
		return value, nil
	case "disable_streaming":
		if value == "tool_calling" {
			return value, nil
		}
		return boolValue(value)
	case "cache":
		b, e := boolValue(value)
		if e != nil {
			return nil, e
		}
		if b == true {
			return nil, fail("No global model response cache is configured")
		}
		return b, nil
	case "rate_limiter", "custom_get_token_ids":
		return nil, fail("This reference field requires a non-JSON Python object")
	}
	return value, nil
}
func boolValue(value any) (any, error) {
	if v, ok := value.(bool); ok {
		return v, nil
	}
	if n, ok := number(value); ok && (n == 0 || n == 1) {
		return n == 1, nil
	}
	if text, ok := value.(string); ok {
		switch strings.ToLower(text) {
		case "true", "1", "on", "yes", "y", "t":
			return true, nil
		case "false", "0", "off", "no", "n", "f":
			return false, nil
		}
	}
	return nil, fmt.Errorf("Invalid boolean provider option")
}

var localOption = map[string]bool{"name": true, "cache": true, "verbose": true, "tags": true, "metadata": true, "custom_get_token_ids": true, "rate_limiter": true, "disable_streaming": true, "output_version": true}
