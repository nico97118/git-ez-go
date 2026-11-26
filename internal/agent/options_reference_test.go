// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"encoding/json"
	"fmt"
	"github.com/nico97118/git-ez-go/internal/config"
	"os"
	"reflect"
	"testing"
)

func TestPinnedOllamaOptionNormalization(t *testing.T) {
	data, e := os.ReadFile("testdata/ollama-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures struct {
		Cases []struct {
			RequestOptions map[string]any `json:"request_options"`
			Wire           map[string]any `json:"wire"`
			Error          string         `json:"error_type"`
		}
	}
	if e = json.Unmarshal(data, &fixtures); e != nil {
		t.Fatal(e)
	}
	for i, tc := range fixtures.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := config.Defaults()
			c.Provider = "ollama"
			c.Model = "fixture"
			c.RequestOptions = tc.RequestOptions
			var wire map[string]any
			e := c.Validate()
			if e == nil {
				s := session{config: c}
				wire, e = nativeOptions(&s)
			}
			if tc.Error != "" {
				if e == nil {
					t.Fatal("reference rejection accepted", tc.RequestOptions)
				}
				return
			}
			if e != nil {
				t.Fatal("reference options rejected", e, tc.RequestOptions)
			}
			b, _ := json.Marshal(wire)
			var normalized map[string]any
			_ = json.Unmarshal(b, &normalized)
			if !reflect.DeepEqual(normalized, tc.Wire) {
				t.Fatal("wire normalization differs", normalized, tc.Wire)
			}
		})
	}
}
