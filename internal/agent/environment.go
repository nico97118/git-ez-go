// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"fmt"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"os"
	"strings"
)

// Git uses shell-quoted words for its inherited command configuration. Decode
// and re-encode that transport, including quotes embedded in credential values.
func filterGitParameters(input string) (string, error) {
	var words []string
	var b strings.Builder
	quote := byte(0)
	started := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				b.WriteByte(ch)
			}
			continue
		}
		if ch == '\\' {
			if i+1 == len(input) {
				return "", fmt.Errorf("Invalid Git configuration parameter quoting")
			}
			i++
			b.WriteByte(input[i])
			started = true
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
			} else {
				b.WriteByte(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			started = true
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if started {
				words = append(words, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteByte(ch)
		started = true
	}
	if quote != 0 {
		return "", fmt.Errorf("Invalid Git configuration parameter quoting")
	}
	if started {
		words = append(words, b.String())
	}
	for i, word := range words {
		key, _, _ := strings.Cut(word, "=")
		if strings.EqualFold(key, "ez.apikey") {
			word = key + "=<redacted>"
		}
		words[i] = "'" + strings.ReplaceAll(word, "'", "'\\''") + "'"
	}
	return strings.Join(words, " "), nil
}

func environmentLookup(environment []string) func(string) (string, bool) {
	if environment == nil {
		return os.LookupEnv
	}
	return func(key string) (string, bool) {
		for _, entry := range environment {
			name, value, ok := strings.Cut(entry, "=")
			if ok && name == key {
				return value, true
			}
		}
		return "", false
	}
}
func (s *session) key() string {
	return s.config.ResolvedAPIKey(environmentLookup(environmentFor(s.stop)))
}

func environmentFor(stop resolver.Stop) []string {
	if stop.Environment == nil {
		return nil
	}
	return stop.Environment()
}
