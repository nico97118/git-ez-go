// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedInvestigationFiltersCredentialsAcrossEvidence(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, channel := range []string{"http", "command-and-response"} {
			t.Run(provider+"/"+channel, func(t *testing.T) {
				secret := "synthetic-failure-channel-credential"
				c, g, f, close := setup(t, provider, command("printf '"+secret+"'; printf '"+secret+"' >&2; exit 7"), step{text: "Cannot resolve: " + secret})
				defer close()
				c.APIKey = &secret
				if channel == "http" {
					f.status = 401
					f.secret = secret
				}
				r, err := runEngine(t, c, g)
				if err == nil || r.Integrated || strings.Contains(err.Error(), secret) {
					t.Fatal(r, err)
				}
				expected := 2
				if channel == "http" {
					expected = 1
				}
				if len(f.requests) != expected {
					t.Fatal("unexpected retry", len(f.requests))
				}
				count, redacted := 0, false
				walkErr := filepath.WalkDir(r.Directory, func(path string, d fs.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if d.IsDir() {
						return nil
					}
					data, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					count++
					if strings.Contains(string(data), secret) {
						t.Errorf("credential leaked in %s", path)
					}
					if strings.Contains(string(data), "<redacted>") {
						redacted = true
					}
					return nil
				})
				if walkErr != nil || count < 5 || (channel == "command-and-response" && !redacted) {
					t.Fatal("missing inspectable failure evidence", count, redacted, walkErr)
				}
				if len(f.auth) != expected || f.auth[0] != "Bearer "+secret {
					t.Fatal("credential not exercised at transport boundary", f.auth)
				}
				if channel == "command-and-response" {
					outputs, _ := filepath.Glob(filepath.Join(r.Directory, "agent-*", "command-*.std*"))
					if len(outputs) != 2 {
						t.Fatal("command diagnostics not retained", outputs)
					}
					for _, path := range outputs {
						data, e := os.ReadFile(path)
						if e != nil || !strings.Contains(string(data), "<redacted>") {
							t.Fatal(path, string(data), e)
						}
					}
				}
			})
		}
	}
}
