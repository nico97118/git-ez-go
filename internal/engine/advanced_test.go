// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestRebaseMultipleStopsAndEmptyResolution(t *testing.T) {
	for _, policy := range []string{"", "drop", "keep", "stop"} {
		t.Run(policy, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Commit(t, g, "file.txt", "second\n", "second")
			if policy != "" {
				text := "main\n"
				f.Edits["file.txt"] = &text
			}
			e := newEngine(t, g, f)
			r, err := e.Run(context.Background(), engine.Request{Operation: "rebase", Target: "main", Rebase: options.Rebase{Empty: policy}})
			if policy == "stop" {
				if err == nil || r.Integrated || len(f.Calls) != 1 {
					t.Fatal(r, err)
				}
				return
			}
			if err != nil || !r.Integrated || len(f.Calls) != 2 {
				t.Fatal(r, err, len(f.Calls))
			}
			if policy == "keep" && testutil.Must(t, g, "rev-list", "--count", "main..HEAD") != "2" {
				t.Fatal("empty commits not retained")
			}
			if policy == "drop" && r.ResultOID != testutil.Must(t, g, "rev-parse", "main") {
				t.Fatal("empty commits not dropped")
			}
		})
	}
}
func TestRebaseOntoAndKeepBase(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{true: "keep-base", false: "onto"}[keep], func(t *testing.T) {
			g, f := fixture(t)
			base := testutil.Must(t, g, "rev-parse", "main~1")
			testutil.Must(t, g, "checkout", "-b", "old", base)
			old := testutil.Commit(t, g, "old.txt", "old\n", "old")
			testutil.Must(t, g, "checkout", "-b", "topic")
			testutil.Commit(t, g, "topic.txt", "topic\n", "topic")
			q := engine.Request{Operation: "rebase", Target: "old"}
			if keep {
				q.Target = "main"
				q.Rebase.KeepBase = options.Enabled
			} else {
				onto := "main"
				q.Onto = &onto
			}
			r := run(t, newEngine(t, g, f), q)
			if len(f.Calls) > 0 {
				t.Fatal("unexpected conflicts")
			}
			if keep {
				if testutil.Must(t, g, "rev-parse", "HEAD~2") != base {
					t.Fatal("base moved")
				}
			} else {
				if testutil.Must(t, g, "rev-parse", "HEAD^") != testutil.Must(t, g, "rev-parse", "main") {
					t.Fatal("onto")
				}
				if _, err := os.Stat(filepath.Join(g.Dir, "old.txt")); !os.IsNotExist(err) {
					t.Fatal("excluded commit replayed")
				}
			}
			if r.Directory != "" || testutil.Must(t, g, "rev-parse", "old") != old {
				t.Fatal("plan or input ref")
			}
		})
	}
}
func TestInitialEmptyForceAndEquivalentReapply(t *testing.T) {
	for _, kind := range []string{"empty-keep", "empty-drop", "force", "equivalent-drop", "equivalent-keep"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "checkout", "-b", "topic", "main")
			q := engine.Request{Operation: "rebase", Target: "main"}
			switch kind {
			case "empty-keep", "empty-drop":
				testutil.Must(t, g, "commit", "--allow-empty", "-m", "empty")
				q.Rebase.KeepEmpty = options.Enabled
				if kind == "empty-drop" {
					q.Rebase.KeepEmpty = options.Disabled
				}
			case "force":
				testutil.Commit(t, g, "other", "other", "other")
				q.Rebase.ForceRebase = options.Enabled
			case "equivalent-drop", "equivalent-keep":
				testutil.Must(t, g, "checkout", "feature")
				testutil.Must(t, g, "checkout", "main")
				testutil.Commit(t, g, "file.txt", "feature\n", "equivalent")
				testutil.Must(t, g, "checkout", "feature")
				q.Rebase.ReapplyCherryPicks = options.Enabled
				q.Rebase.Empty = "drop"
				if kind == "equivalent-keep" {
					q.Rebase.Empty = "keep"
				}
			}
			if kind == "force" {
				g.Env = testutil.SetEnv(g.Env, "GIT_COMMITTER_DATE", "2001-01-01T00:00:00Z")
			}
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			r := run(t, newEngine(t, g, f), q)
			if len(f.Calls) > 0 {
				t.Fatal("called resolver")
			}
			if kind == "force" && r.ResultOID == before {
				t.Fatal("force did not replay")
			}
			if strings.HasSuffix(kind, "-keep") && testutil.Must(t, g, "rev-list", "--count", "main..HEAD") != "1" {
				t.Fatal("keep failed")
			}
		})
	}
}
func TestAutosquashCapturesNativeSequenceAndIntegrity(t *testing.T) {
	for _, marker := range []string{"fixup! ", "squash! ", "amend! "} {
		t.Run(marker, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "checkout", "-b", "topic", "main")
			testutil.Commit(t, g, "topic", "one", "subject")
			testutil.Commit(t, g, "topic", "two", marker+"subject")
			testutil.Must(t, g, "config", "rebase.instructionFormat", "%s%nexec false")
			r := run(t, newEngine(t, g, f), engine.Request{Operation: "rebase", Target: "main", Rebase: options.Rebase{Autosquash: options.Enabled}})
			if len(f.Calls) > 0 || testutil.Must(t, g, "rev-list", "--count", "main..HEAD") != "1" {
				t.Fatal("not grouped")
			}
			if r.Directory != "" {
				t.Fatal("native autosquash created a persistent LLM audit")
			}

		})
	}
}
func TestSequenceEvidenceFailureNeverClaimsSuccess(t *testing.T) {
	for _, kind := range []string{"limit", "resolver-tamper", "validation-tamper"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			g, f := fixture(t)
			testutil.Commit(t, g, "other", "one", strings.Repeat("x", 300))
			q := engine.Request{Operation: "rebase", Target: "main", Rebase: options.Rebase{Autosquash: options.Enabled}}
			var rr resolver.Resolver = f
			if kind == "resolver-tamper" {
				rr = resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
					d, e := f.Resolve(ctx, s)
					captures, err := filepath.Glob(filepath.Join(os.TempDir(), "git-ez-engine-*", "rebase-todo.txt"))
					if err != nil || len(captures) != 1 {
						t.Fatal(captures, err)
					}
					if er := os.WriteFile(captures[0], []byte("tampered"), 0600); er != nil {
						t.Fatal(er)
					}
					return d, e
				})
			}
			if kind == "validation-tamper" {
				q.Validation = []string{"sh", "-c", `for p in "$TMPDIR"/git-ez-engine-*/rebase-todo.txt.sha256; do printf tampered > "$p"; done`}
			}
			e := newEngine(t, g, rr)
			if kind == "limit" {
				e.Config.MaxFileBytes = 32
			}
			r, err := e.Run(context.Background(), q)
			if err == nil || r.Status == "succeeded" {
				t.Fatal(r, err)
			}
			if kind == "limit" && len(f.Calls) > 0 {
				t.Fatal("replayed after failed capture")
			}
			if kind == "validation-tamper" && !r.Integrated {
				t.Fatal("completion lost")
			}
		})
	}
}
func TestNativeContinuationOwnershipAndTransfer(t *testing.T) {
	for _, backend := range []string{"merge", "apply"} {
		for _, second := range []bool{false, true} {
			t.Run(backend+map[bool]string{true: "-new-stop", false: "-complete"}[second], func(t *testing.T) {
				g, f := fixture(t)
				if second {
					testutil.Commit(t, g, "file.txt", "second\n", "second")
				}
				r, e := g.Run(context.Background(), "rebase", "--"+backend, "main")
				if e != nil || r.ExitCode == 0 {
					t.Fatal(r, e)
				}
				index := testutil.Must(t, g, "ls-files", "--stage")
				en := newEngine(t, g, f)
				pending, e := en.Run(context.Background(), engine.Request{Operation: "rebase", Continue: true})
				if e != nil || pending.NativeStatus == nil || *pending.NativeStatus == 0 || len(f.Calls) > 0 || testutil.Must(t, g, "ls-files", "--stage") != index {
					t.Fatal("adopted manual stop", pending, e)
				}
				testutil.Write(t, g, "file.txt", "manual\n")
				testutil.Must(t, g, "add", "file.txt")
				done, e := en.Run(context.Background(), engine.Request{Operation: "rebase", Continue: true})
				if e != nil {
					t.Fatal(done, e)
				}
				if second {
					if !done.Integrated || len(f.Calls) != 1 || *f.Calls[0].Conflicts[0].Theirs != "second\n" {
						t.Fatal("new stop not adopted", done, f.Calls)
					}
				} else if done.NativeStatus == nil || *done.NativeStatus != 0 || len(f.Calls) > 0 {
					t.Fatal(done)
				}
			})
		}
	}
}
func TestMergeAndPickManualContinuationAndFinalValidation(t *testing.T) {
	for _, op := range []string{"merge", "cherry-pick"} {
		for _, check := range []bool{false, true} {
			t.Run(op+map[bool]string{true: "-check", false: "-native"}[check], func(t *testing.T) {
				g, f := fixture(t)
				r, e := g.Run(context.Background(), op, "main")
				if e != nil || r.ExitCode == 0 {
					t.Fatal(r, e)
				}
				testutil.Write(t, g, "file.txt", "manual\n")
				testutil.Must(t, g, "add", "file.txt")
				q := engine.Request{Operation: op, Continue: true}
				if check {
					q.Validation = []string{"sh", "-c", "exit 0"}
				}
				r2, e := newEngine(t, g, f).Run(context.Background(), q)
				if e != nil || len(f.Calls) > 0 {
					t.Fatal(r2, e)
				}
				if check && !r2.Integrated {
					t.Fatal("no completion metadata")
				}
				if !check && (r2.NativeStatus == nil || *r2.NativeStatus != 0) {
					t.Fatal(r2)
				}
			})
		}
	}
}
func TestValidationPreservesCompletionAndDetectsMutations(t *testing.T) {
	for _, kind := range []string{"failure", "dirty", "head", "target", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "main", Validation: []string{"sh", "-c", "exit 17"}}
			switch kind {
			case "dirty":
				q.Validation[2] = "echo changed > file.txt"
			case "head":
				q.Validation = []string{"git", "reset", "--hard", "HEAD^1"}
			case "target":
				q.Validation = []string{"git", "update-ref", "refs/heads/main", "HEAD"}
			case "timeout":
				q.Validation = []string{"sh", "-c", "echo partial; sleep 20"}
			}
			en := newEngine(t, g, f)
			if kind == "timeout" {
				en.Config.Timeout = .1
			}
			r, e := en.Run(context.Background(), q)
			if e == nil || !r.Integrated || view(t, r)["integrated"] != true {
				t.Fatal("completion vs success", r, e)
			}
			if active, _ := g.Active(context.Background()); active {
				t.Fatal("operation restarted")
			}
		})
	}
}
