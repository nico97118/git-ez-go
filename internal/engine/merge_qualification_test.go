// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestMergeMessageCleanupAndLogMatchNativeGit(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, cleanup := range []string{"strip", "whitespace", "verbatim", "scissors", "default"} {
			for _, limit := range []int64{0, 1, 20} {
				t.Run(fmt.Sprintf("%t/%s/%d", conflict, cleanup, limit), func(t *testing.T) {
					g, f := fixture(t)
					if !conflict {
						testutil.Must(t, g, "checkout", "-b", "clean-message", "main~1")
						testutil.Commit(t, g, "independent", "new\n", "independent")
					}
					branch := testutil.Must(t, g, "branch", "--show-current")
					before := testutil.Must(t, g, "rev-parse", "HEAD")
					target := testutil.Must(t, g, "rev-parse", "main")
					message := "  Merge title  \n\n# custom comment\n\nDetails   \n"
					testutil.Must(t, g, "checkout", "-b", "native-message", before)
					native, err := g.Run(context.Background(), "merge", "--no-edit", fmt.Sprintf("--log=%d", limit), "--cleanup="+cleanup, "--signoff", "-m", message, target)
					if err != nil {
						t.Fatal(err)
					}
					if conflict {
						if native.ExitCode == 0 {
							t.Fatal("missing native conflict")
						}
						testutil.Write(t, g, "file.txt", "combined\n")
						testutil.Must(t, g, "add", "file.txt")
						testutil.Must(t, g, "commit", "--no-edit", "--signoff", "--cleanup="+cleanup)
					} else if native.ExitCode != 0 {
						t.Fatal(string(native.Stderr))
					}
					expected, err := g.Checked(context.Background(), "show", "-s", "--format=%B", "HEAD")
					if err != nil {
						t.Fatal(err)
					}
					testutil.Must(t, g, "checkout", branch)
					run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", Commit: options.Commit{Message: &message, Log: big.NewInt(limit), Cleanup: cleanup, Signoff: options.Enabled}})
					actual, err := g.Checked(context.Background(), "show", "-s", "--format=%B", "HEAD")
					if err != nil || !bytes.Equal(actual.Stdout, expected.Stdout) {
						t.Fatalf("native message differs: expected %q, got %q (%v)", expected.Stdout, actual.Stdout, err)
					}
					calls := 0
					if conflict {
						calls = 1
					}
					if len(f.Calls) != calls {
						t.Fatal("wrong resolver eligibility", len(f.Calls))
					}
				})
			}
		}
	}
}
func TestMergeNativeConfigurationAndExplicitOverrides(t *testing.T) {
	cases := []struct {
		policy, configured string
		merge              bool
	}{{"", "", false}, {"", "false", true}, {"", "only", false}, {"ff", "false", false}, {"no-ff", "only", true}, {"ff-only", "false", false}}
	for i, c := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "checkout", "-b", "behind", "main~1")
			base := testutil.Must(t, g, "rev-parse", "HEAD")
			target := testutil.Must(t, g, "rev-parse", "main")
			if c.configured != "" {
				testutil.Must(t, g, "config", "merge.ff", c.configured)
			}
			run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", MergeFF: c.policy})
			if c.merge {
				if testutil.Must(t, g, "rev-parse", "HEAD^1") != base || testutil.Must(t, g, "rev-parse", "HEAD^2") != target {
					t.Fatal("wrong merge parents")
				}
			} else if testutil.Must(t, g, "rev-parse", "HEAD") != target {
				t.Fatal("not fast-forwarded")
			}
			if len(f.Calls) != 0 {
				t.Fatal("model used for native integration")
			}
		})
	}
	for _, policy := range []string{"ff", "no-ff"} {
		t.Run("conflict/"+policy, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "config", "merge.ff", "only")
			run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", MergeFF: policy})
			if len(f.Calls) != 1 {
				t.Fatal(len(f.Calls))
			}
		})
	}
	for _, mode := range []string{"message", "log-cleanup"} {
		t.Run("fast-forward/"+mode, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "checkout", "-b", "behind", "main~1")
			target := testutil.Must(t, g, "rev-parse", "main")
			message := "unused"
			c := options.Commit{Message: &message, Signoff: options.Enabled}
			if mode == "log-cleanup" {
				c = options.Commit{Log: big.NewInt(20), Cleanup: "strip"}
			}
			run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", Commit: c})
			if testutil.Must(t, g, "rev-parse", "HEAD") != target || len(f.Calls) != 0 || strings.Contains(testutil.Must(t, g, "show", "-s", "--format=%B", "HEAD"), "Signed-off-by:") {
				t.Fatal("message options forced a commit")
			}
		})
	}
}
func TestMergeLogConfigurationAndMultipleSubjectLimits(t *testing.T) {
	for _, limit := range []int64{-1, 0, 1, 20} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "config", "merge.log", "true")
			testutil.Must(t, g, "checkout", "main")
			testutil.Must(t, g, "commit", "--allow-empty", "-m", "second incoming")
			testutil.Must(t, g, "commit", "--allow-empty", "-m", "third incoming")
			testutil.Must(t, g, "checkout", "feature")
			message := "Configured summary"
			c := options.Commit{Message: &message, Signoff: options.Enabled}
			if limit >= 0 {
				c.Log = big.NewInt(limit)
			}
			run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", Commit: c})
			body := testutil.Must(t, g, "show", "-s", "--format=%B", "HEAD")
			if !strings.HasPrefix(body, "Configured summary\n") || strings.Count(body, "Signed-off-by:") != 1 || strings.Contains(body, "  third incoming") != (limit != 0) || strings.Contains(body, "  second incoming") != (limit < 0 || limit > 1) || strings.Contains(body, "  main") != (limit < 0 || limit > 1) {
				t.Fatal(body)
			}
			if testutil.Must(t, g, "config", "merge.log") != "true" {
				t.Fatal("changed native config")
			}
		})
	}
	t.Run("commit-hook-failure", func(t *testing.T) {
		g, f := fixture(t)
		before := testutil.Must(t, g, "rev-parse", "HEAD")
		if err := os.WriteFile(filepath.Join(g.Dir, ".git/hooks/commit-msg"), []byte("#!/bin/sh\nexit 23\n"), 0700); err != nil {
			t.Fatal(err)
		}
		r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Commit: options.Commit{Log: big.NewInt(1), Cleanup: "strip"}})
		active, _ := g.Active(context.Background())
		if err == nil || r.Integrated || !active || testutil.Must(t, g, "rev-parse", "HEAD") != before {
			t.Fatal(r, err, active)
		}
		raw, err := os.ReadFile(filepath.Join(g.Dir, ".git/MERGE_MSG"))
		if err != nil || !strings.Contains(string(raw), "main") {
			t.Fatal(string(raw), err)
		}
	})
}
