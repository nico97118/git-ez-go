// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/resolver"
)

func TestOptionalValidationDiagnosticsPreserveCheckAndIntegrationStatus(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		for _, stream := range []string{"stdout", "stderr"} {
			for _, exit := range []int{0, 7} {
				t.Run(fmt.Sprintf("interactive=%v/%s/exit=%d", interactive, stream, exit), func(t *testing.T) {
					g, f := fixture(t)
					wrapped := resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
						if err := os.MkdirAll(filepath.Join(stop.ArtifactDirectory, "validation."+stream), 0700); err != nil {
							return resolver.Decision{}, err
						}
						return f.Resolve(ctx, stop)
					})
					e := newEngine(t, g, wrapped)
					e.Config.ValidationAttempts = 1
					key := "diagnostic-secret"
					e.Config.APIKey = &key
					e.In = strings.NewReader("a\nq\n")
					var warnings bytes.Buffer
					e.Err = &warnings
					result, err := e.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: interactive, Validation: []string{"/bin/sh", "-c", fmt.Sprintf("printf 'available diagnostic-secret'; printf 'available diagnostic-secret' >&2; exit %d", exit)}})
					if (err == nil) != (exit == 0) {
						t.Fatal(result, err)
					}
					if result.Integrated != (!interactive || exit == 0) {
						t.Fatal(result, err)
					}
					data := view(t, result)
					v := data["validation"].(map[string]any)
					want := "passed"
					if exit != 0 {
						want = "failed"
					}
					if v["status"] != want || v["returncode"] != float64(exit) || v["diagnostics_incomplete"] != true || v[stream+"_available"] != false {
						t.Fatal(v)
					}
					preview, _ := v[stream+"_preview"].(string)
					if !strings.Contains(preview, "available <redacted>") || strings.Contains(preview, key) || !strings.Contains(warnings.String(), "Rerun") {
						t.Fatal(v, warnings.String())
					}
					if exit == 0 && data["status"] != "succeeded" {
						t.Fatal(data)
					}
					if interactive && exit != 0 && result.ResolverCalls != 1 {
						t.Fatal("Failed validation was accepted or retried beyond policy", result)
					}
				})
			}
		}
	}
}

func TestOptionalAttemptDiagnosticCopyDoesNotBlockAcceptance(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			g, f := fixture(t)
			wrapped := resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
				data, err := os.ReadFile(filepath.Join(stop.ArtifactDirectory, "run.json"))
				if err != nil {
					return resolver.Decision{}, err
				}
				var run map[string]any
				if err = json.Unmarshal(data, &run); err != nil {
					return resolver.Decision{}, err
				}
				reviews := run["reviews"].([]any)
				attempt := reviews[len(reviews)-1].(map[string]any)["artifacts"].(string)
				if err = os.Mkdir(filepath.Join(stop.ArtifactDirectory, attempt, "validation."+stream), 0700); err != nil {
					return resolver.Decision{}, err
				}
				return f.Resolve(ctx, stop)
			})
			e := newEngine(t, g, wrapped)
			e.In = strings.NewReader("a\n")
			var warnings bytes.Buffer
			e.Err = &warnings
			result, err := e.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true, Validation: []string{"/bin/sh", "-c", "printf stdout; printf stderr >&2"}})
			if err != nil || !result.Integrated {
				t.Fatal(result, err)
			}
			data := view(t, result)
			attempt := data["reviews"].([]any)[0].(map[string]any)
			v := attempt["validation"].(map[string]any)
			if v["status"] != "passed" || v[stream+"_available"] != false || v["diagnostics_incomplete"] != true || !strings.Contains(warnings.String(), "incomplete") {
				t.Fatal(v, warnings.String())
			}
			if data["validation"].(map[string]any)[stream+"_available"] != true {
				t.Fatal("Available root evidence was discarded")
			}
		})
	}
}
