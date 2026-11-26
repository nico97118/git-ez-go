// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestPinnedSelectionMatchesNativeGit(t *testing.T) {
	cases := []struct {
		keep, fork, diverged, equivalent, autosquash bool
		reapply                                      options.Toggle
	}{{false, true, false, false, false, options.Unspecified}, {false, true, true, true, false, options.Unspecified}, {false, true, true, true, false, options.Enabled}, {true, false, true, true, false, options.Unspecified}, {true, true, true, true, false, options.Unspecified}, {true, true, true, true, false, options.Disabled}, {true, false, true, true, false, options.Disabled}, {false, true, true, true, true, options.Unspecified}, {true, true, true, false, true, options.Unspecified}}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			g, f := fixture(t)
			base := testutil.Must(t, g, "rev-parse", "main~1")
			testutil.Must(t, g, "checkout", "-b", "upstream", base)
			testutil.Commit(t, g, "old.txt", "old\n", "old upstream")
			testutil.Must(t, g, "checkout", "-b", "topic")
			feature := testutil.Commit(t, g, "a.txt", "A\n", "feature A")
			testutil.Commit(t, g, "b.txt", "B\n", "feature B")
			if c.autosquash {
				testutil.Commit(t, g, "a.txt", "fixed A\n", "fixup! feature A")
			}
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "checkout", "upstream")
			testutil.Must(t, g, "reset", "--hard", base)
			if c.diverged {
				testutil.Commit(t, g, "new.txt", "new\n", "new upstream")
			}
			if c.equivalent {
				testutil.Must(t, g, "cherry-pick", feature)
			}
			testutil.Must(t, g, "checkout", "-b", "native", before)
			g.Env = testutil.SetEnv(g.Env, "GIT_SEQUENCE_EDITOR", "true")
			args := []string{"rebase", "--merge", "--no-autostash", "--no-update-refs", "--no-rebase-merges", "--empty=keep"}
			if c.keep {
				args = append(args, "--keep-base")
			}
			fork := options.Disabled
			if c.fork {
				fork = options.Enabled
				args = append(args, "--fork-point")
			} else {
				args = append(args, "--no-fork-point")
			}
			// The pinned Python contract explicitly enables reapplication for
			// keep-base when omitted. Git 2.38 has an older native default,
			// so compare identical effective policies across Git versions.
			if c.reapply == options.Enabled || (c.keep && c.reapply == options.Unspecified) {
				args = append(args, "--reapply-cherry-picks")
			} else if c.reapply == options.Disabled {
				args = append(args, "--no-reapply-cherry-picks")
			}
			if c.autosquash {
				args = append(args, "--interactive", "--autosquash")
			}
			testutil.Must(t, g, append(args, "upstream")...)
			tree := testutil.Must(t, g, "rev-parse", "HEAD^{tree}")
			messages := testutil.Must(t, g, "log", "--format=%B%x00", "HEAD")
			testutil.Must(t, g, "checkout", "topic")
			refs := testutil.Must(t, g, "for-each-ref", "--format=%(refname)")
			keep := options.Disabled
			if c.keep {
				keep = options.Enabled
			}
			autosquash := options.Disabled
			if c.autosquash {
				autosquash = options.Enabled
			}
			run(t, newEngine(t, g, f), engine.Request{Operation: "rebase", Target: "upstream", Rebase: options.Rebase{Empty: "keep", KeepBase: keep, ForkPoint: fork, ReapplyCherryPicks: c.reapply, Autosquash: autosquash}})
			if tree != testutil.Must(t, g, "rev-parse", "HEAD^{tree}") || messages != testutil.Must(t, g, "log", "--format=%B%x00", "HEAD") || refs != testutil.Must(t, g, "for-each-ref", "--format=%(refname)") || len(f.Calls) > 0 {
				t.Fatalf("pinned selection differs from native Git: tree %s -> %s; messages %q -> %q; refs %q -> %q; resolver calls %d", tree, testutil.Must(t, g, "rev-parse", "HEAD^{tree}"), messages, testutil.Must(t, g, "log", "--format=%B%x00", "HEAD"), refs, testutil.Must(t, g, "for-each-ref", "--format=%(refname)"), len(f.Calls))
			}
		})
	}
}
func TestForkPointAdmissionAndFallback(t *testing.T) {
	for _, target := range []string{"HEAD~1", "main"} {
		t.Run(target, func(t *testing.T) {
			g, f := fixture(t)
			en := newEngine(t, g, f)
			r, e := en.Run(context.Background(), engine.Request{Operation: "rebase", Target: target, Rebase: options.Rebase{ForkPoint: options.Enabled}})
			if target == "HEAD~1" {
				if e == nil || r.Directory != "" {
					t.Fatal(r, e)
				}
			} else if e != nil {
				t.Fatal(r, e)
			}
		})
	}
}
func TestRerereReuseHasNoResolverOrHookRetry(t *testing.T) {
	for _, op := range []string{"merge", "cherry-pick", "rebase"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "config", "rerere.enabled", "true")
			r, e := g.Run(context.Background(), op, "main")
			if e != nil || r.ExitCode == 0 {
				t.Fatal(r, e)
			}
			testutil.Write(t, g, "file.txt", "combined\n")
			testutil.Must(t, g, "add", "file.txt")
			if op == "rebase" {
				testutil.Must(t, g, "rebase", "--continue")
			} else {
				testutil.Must(t, g, "commit", "--no-edit")
			}
			testutil.Must(t, g, "reset", "--hard", before)
			r2 := run(t, newEngine(t, g, f), engine.Request{Operation: op, Target: "main", Rerere: options.Enabled, Interactive: true})
			if len(f.Calls) > 0 || r2.ResolverCalls != 0 || r2.Directory != "" {
				t.Fatal("rerere reuse invoked resolver")
			}
			setting, settingErr := g.Run(context.Background(), "config", "--get", "rerere.autoupdate")
			if settingErr != nil {
				t.Fatal(settingErr)
			}
			if len(setting.Stdout) != 0 {
				t.Fatal("override persisted")
			}
		})
	}
}
func TestMergePreparedMessageAndSupportingText(t *testing.T) {
	g, f := fixture(t)
	message := "prepared subject"
	f.Edits["support.txt"] = &message
	r := run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main", Commit: options.Commit{Message: &message, Signoff: options.Enabled, Cleanup: "scissors"}})
	text := testutil.Must(t, g, "show", "-s", "--format=%B", "HEAD")
	if !strings.Contains(text, "prepared subject") || !strings.Contains(text, "Signed-off-by:") {
		t.Fatal(text)
	}
	if testutil.Must(t, g, "show", "HEAD:support.txt") != message {
		t.Fatal("supporting text not staged")
	}
	if _, e := os.Stat(filepath.Join(r.Directory, "proposal-0001", "files.json")); e != nil {
		t.Fatal("proposal not retained", e)
	}
}
