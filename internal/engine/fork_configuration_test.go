// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestForkPointConfigurationWithExplicitUpstreamMatchesNativeGit(t *testing.T) {
	for _, keep := range []bool{false, true} {
		for _, policy := range []struct {
			configured bool
			requested  options.Toggle
		}{{true, options.Unspecified}, {false, options.Unspecified}, {false, options.Enabled}, {true, options.Disabled}} {
			t.Run(fmt.Sprintf("%t/%t/%d", keep, policy.configured, policy.requested), func(t *testing.T) {
				g := testutil.Repo(t)
				base := testutil.Commit(t, g, "base", "base\n", "base")
				testutil.Must(t, g, "checkout", "-b", "upstream")
				testutil.Commit(t, g, "old.txt", "old\n", "old upstream")
				testutil.Must(t, g, "checkout", "-b", "topic")
				testutil.Commit(t, g, "a.txt", "A\n", "feature A")
				before := testutil.Commit(t, g, "b.txt", "B\n", "feature B")
				testutil.Must(t, g, "checkout", "upstream")
				testutil.Must(t, g, "reset", "--hard", base)
				testutil.Must(t, g, "config", "rebase.forkPoint", strconv.FormatBool(policy.configured))
				testutil.Must(t, g, "checkout", "-b", "native-configured", before)
				args := []string{"rebase", "--merge", "--no-autostash", "--no-update-refs"}
				if keep {
					args = append(args, "--keep-base")
				}
				if policy.requested == options.Enabled {
					args = append(args, "--fork-point")
				} else if policy.requested == options.Disabled {
					args = append(args, "--no-fork-point")
				}
				args = append(args, "upstream")
				testutil.Must(t, g, args...)
				expectedTree := testutil.Must(t, g, "rev-parse", "HEAD^{tree}")
				expectedMessages := testutil.Must(t, g, "log", "--format=%B%x00", "upstream..HEAD")
				testutil.Must(t, g, "checkout", "topic")
				f := &testutil.FakeResolver{}
				keepPolicy := options.Disabled
				if keep {
					keepPolicy = options.Enabled
				}
				run(t, newEngine(t, g, f), engine.Request{Operation: "rebase", Target: "upstream", Rebase: options.Rebase{KeepBase: keepPolicy, ForkPoint: policy.requested}})
				if len(f.Calls) != 0 || testutil.Must(t, g, "rev-parse", "HEAD^{tree}") != expectedTree || testutil.Must(t, g, "log", "--format=%B%x00", "upstream..HEAD") != expectedMessages {
					t.Fatal("native fork selection differed")
				}
				count := "3"
				if policy.requested == options.Enabled {
					count = "2"
				}
				_, err := os.Stat(filepath.Join(g.Dir, "old.txt"))
				if testutil.Must(t, g, "rev-list", "--count", "upstream..HEAD") != count || os.IsNotExist(err) != (policy.requested == options.Enabled) || testutil.Must(t, g, "config", "rebase.forkPoint") != strconv.FormatBool(policy.configured) {
					t.Fatal("fork configuration/default changed", err)
				}
				if active, err := g.Active(context.Background()); err != nil || active {
					t.Fatal(active, err)
				}
			})
		}
	}
}
