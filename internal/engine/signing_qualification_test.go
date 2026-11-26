// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func qualificationSigning(t *testing.T, g git.Client) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed")
	if err = os.WriteFile(allowed, append([]byte("test@example.invalid "), public...), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Must(t, g, "config", "gpg.format", "ssh")
	testutil.Must(t, g, "config", "user.signingkey", key)
	testutil.Must(t, g, "config", "gpg.ssh.allowedSignersFile", allowed)
	return key
}
func TestSigningControlsAndEmptyCommitPolicies(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, mode := range []string{"explicit-key", "configured-key", "no-sign"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				g, f := fixture(t)
				c := options.Commit{Signing: options.Signing{Policy: options.Enabled}, Signoff: options.Enabled}
				if mode == "no-sign" {
					testutil.Must(t, g, "config", "commit.gpgsign", "true")
					testutil.Must(t, g, "config", "gpg.program", "/does-not-exist/git-ez-signing")
					c.Signing.Policy = options.Disabled
					c.Signoff = options.Disabled
				} else {
					key := qualificationSigning(t, g)
					if mode == "explicit-key" {
						c.Signing.Key = key
						testutil.Must(t, g, "config", "user.signingkey", "/does-not-exist/default-key")
					}
				}
				run(t, newEngine(t, g, f), engine.Request{Operation: op, Target: "main", Commit: c})
				body := testutil.Must(t, g, "cat-file", "commit", "HEAD")
				if mode == "no-sign" {
					if strings.Contains(body, "gpgsig ") || strings.Contains(body, "Signed-off-by:") {
						t.Fatal(body)
					}
				} else {
					testutil.Must(t, g, "verify-commit", "HEAD")
					if !strings.Contains(body, "Signed-off-by: Test <test@example.invalid>") || len(f.Calls) != 1 {
						t.Fatal(body, len(f.Calls))
					}
				}
			})
		}
	}
	t.Run("retained-empty", func(t *testing.T) {
		g, f := fixture(t)
		key := qualificationSigning(t, g)
		text := "main\n"
		f.Edits["file.txt"] = &text
		run(t, newEngine(t, g, f), engine.Request{Operation: "rebase", Target: "main", Rebase: options.Rebase{Empty: "keep"}, Commit: options.Commit{Signing: options.Signing{Policy: options.Enabled, Key: key}, Signoff: options.Enabled}})
		testutil.Must(t, g, "verify-commit", "HEAD")
		if testutil.Must(t, g, "diff", "HEAD^", "HEAD") != "" || !strings.Contains(testutil.Must(t, g, "show", "-s", "--format=%B", "HEAD"), "Signed-off-by:") {
			t.Fatal("empty commit/signoff lost")
		}
	})
	for _, op := range []string{"merge", "rebase"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failing-signer/%t", op, explicit), func(t *testing.T) {
				g, f := fixture(t)
				testutil.Must(t, g, "config", "gpg.program", "/does-not-exist/git-ez-signing")
				c := options.Commit{}
				if explicit {
					c.Signing.Policy = options.Enabled
				} else {
					testutil.Must(t, g, "config", "commit.gpgsign", "true")
				}
				r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: op, Target: "main", Commit: c})
				active, _ := g.IntegrationActive(context.Background(), op)
				if err == nil || r.Integrated || !active || len(f.Calls) != 1 {
					t.Fatal(r, err, active, len(f.Calls))
				}
			})
		}
	}
}
func TestMergeTargetSignatureVerificationPolicies(t *testing.T) {
	for _, mode := range []string{"signed", "unsigned", "explicit-disable"} {
		t.Run(mode, func(t *testing.T) {
			g, f := fixture(t)
			c := options.Commit{VerifySignatures: options.Enabled}
			if mode == "signed" {
				qualificationSigning(t, g)
				testutil.Must(t, g, "checkout", "main")
				testutil.Must(t, g, "commit", "--allow-empty", "-S", "-m", "signed target")
				testutil.Must(t, g, "checkout", "feature")
			}
			if mode == "explicit-disable" {
				testutil.Must(t, g, "config", "merge.verifySignatures", "true")
				c.VerifySignatures = options.Disabled
			}
			target := testutil.Must(t, g, "rev-parse", "main")
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Commit: c})
			if mode == "unsigned" {
				active, _ := g.Active(context.Background())
				if err == nil || r.Integrated || len(f.Calls) != 0 || active || testutil.Must(t, g, "rev-parse", "HEAD") != before {
					t.Fatal(r, err, active)
				}
			} else if err != nil || !r.Integrated || testutil.Must(t, g, "rev-parse", "HEAD^2") != target {
				t.Fatal(r, err)
			}
		})
	}
}
func TestConfiguredNativeHooksCannotBeBypassed(t *testing.T) {
	for _, op := range []string{"merge", "rebase"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			hook := "pre-rebase"
			if op == "merge" {
				hook = "pre-merge-commit"
				testutil.Must(t, g, "checkout", "-b", "clean", "main~1")
				testutil.Commit(t, g, "other", "other", "other")
			}
			hooks := filepath.Join(g.Dir, ".git", "qualification-hooks")
			if err := os.Mkdir(hooks, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\nprintf blocked >&2\nexit 23\n"), 0700); err != nil {
				t.Fatal(err)
			}
			testutil.Must(t, g, "config", "core.hooksPath", hooks)
			r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: op, Target: "main"})
			if err == nil || r.Integrated || len(f.Calls) != 0 {
				t.Fatal("hook bypassed", r, err, len(f.Calls))
			}
		})
	}
}
