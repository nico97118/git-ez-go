// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/sequence"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
	if len(os.Args) > 1 && os.Args[1] == sequence.Helper {
		os.Exit(sequence.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

type resolverFunc func(context.Context, resolver.Stop) (resolver.Decision, error)

func (f resolverFunc) Resolve(c context.Context, s resolver.Stop) (resolver.Decision, error) {
	return f(c, s)
}
func fixture(t *testing.T) (git.Client, *testutil.FakeResolver) {
	t.Helper()
	g := testutil.Repo(t)
	testutil.Commit(t, g, "file.txt", "base\n", "base")
	testutil.Must(t, g, "checkout", "-b", "feature")
	testutil.Commit(t, g, "file.txt", "feature\n", "feature")
	testutil.Must(t, g, "checkout", "main")
	testutil.Commit(t, g, "file.txt", "main\n", "main")
	testutil.Must(t, g, "checkout", "feature")
	g.Env = testutil.SetEnv(g.Env, "GIT_EDITOR", "true")
	text := "combined\n"
	f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Combine both intentions", Confidence: .95}}
	return g, f
}
func newEngine(t *testing.T, g git.Client, r resolver.Resolver) *engine.Engine {
	t.Helper()
	c := config.Defaults()
	c.Model = "deterministic"
	e, err := engine.New(context.Background(), g, c, r)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func run(t *testing.T, e *engine.Engine, q engine.Request) engine.Result {
	t.Helper()
	r, err := e.Run(context.Background(), q)
	if err != nil || r.Status != "succeeded" || !r.Integrated {
		t.Fatalf("run %+v: %+v %v", q, r, err)
	}
	return r
}
func view(t *testing.T, r engine.Result) map[string]any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(r.Directory, "run.json"))
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestNativeOperationsResolveAndVerifyTopology(t *testing.T) {
	for _, op := range []string{"merge", "cherry-pick", "rebase"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			target := testutil.Must(t, g, "rev-parse", "main")
			r := run(t, newEngine(t, g, f), engine.Request{Operation: op, Target: "main"})
			if len(f.Calls) != 1 || r.ResolverCalls != 1 || f.Calls[0].BeforeOID != before || f.Calls[0].Target.OID != target {
				t.Fatal("resolver boundary", f.Calls)
			}
			if op == "merge" {
				if testutil.Must(t, g, "rev-parse", "HEAD^1") != before || testutil.Must(t, g, "rev-parse", "HEAD^2") != target {
					t.Fatal("merge topology")
				}
			} else if op == "rebase" {
				if testutil.Must(t, g, "rev-parse", "HEAD^") != target {
					t.Fatal("rebase topology")
				}
			} else if testutil.Must(t, g, "rev-parse", "HEAD^") != before {
				t.Fatal("cherry-pick topology")
			}
			if view(t, r)["integrated"] != true || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("not completed")
			}
		})
	}
}
func TestCleanOperationsNeverInvokeResolver(t *testing.T) {
	for _, op := range []string{"merge", "cherry-pick", "rebase"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Must(t, g, "checkout", "-b", "clean", "main~1")
			testutil.Commit(t, g, "other.txt", "other\n", "other")
			r := run(t, newEngine(t, g, f), engine.Request{Operation: op, Target: "main"})
			if r.ResolverCalls != 0 || len(f.Calls) != 0 {
				t.Fatal("unnecessary resolver")
			}
		})
	}
}
func TestAdmissionRejectsBeforeJournalAndMutation(t *testing.T) {
	for _, kind := range []string{"dirty", "untracked", "detached", "active", "invalid-target", "merge-pick", "rebase-merges", "keep-base-onto"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "main"}
			switch kind {
			case "dirty":
				testutil.Write(t, g, "file.txt", "dirty\n")
			case "untracked":
				testutil.Write(t, g, "untracked", "new")
			case "detached":
				testutil.Must(t, g, "checkout", "--detach")
			case "active":
				testutil.Write(t, g, ".git/REVERT_HEAD", testutil.Must(t, g, "rev-parse", "HEAD"))
			case "invalid-target":
				q.Target = "--help"
			case "merge-pick":
				testutil.Must(t, g, "merge", "-s", "ours", "--no-edit", "main")
				q.Operation = "cherry-pick"
				q.Target = "HEAD"
			case "rebase-merges":
				testutil.Must(t, g, "branch", "base", "main~1")
				testutil.Must(t, g, "merge", "-s", "ours", "--no-edit", "main")
				q.Operation = "rebase"
				q.Target = "base"
			case "keep-base-onto":
				q.Operation = "rebase"
				q.Onto = &q.Target
				q.Rebase.KeepBase = options.Enabled
			}
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			r, e := newEngine(t, g, f).Run(context.Background(), q)
			if e == nil || r.Directory != "" || len(f.Calls) > 0 {
				t.Fatal(r, e)
			}
			if before != testutil.Must(t, g, "rev-parse", "HEAD") {
				t.Fatal("mutated on admission")
			}
			if _, e = os.Stat(filepath.Join(g.Dir, ".git/ez")); !os.IsNotExist(e) {
				t.Fatal("premature audit")
			}
		})
	}
}
func TestMergeFastForwardPoliciesAndAlreadyUpToDate(t *testing.T) {
	for _, policy := range []string{"", "ff", "no-ff", "ff-only"} {
		for _, upToDate := range []bool{false, true} {
			t.Run(policy+map[bool]string{true: "-uptodate", false: "-behind"}[upToDate], func(t *testing.T) {
				g, f := fixture(t)
				if !upToDate {
					testutil.Must(t, g, "checkout", "-b", "behind", "main~1")
				}
				target := "main"
				if upToDate {
					target = "HEAD"
				}
				before := testutil.Must(t, g, "rev-parse", "HEAD")
				r := run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: target, MergeFF: policy})
				if len(f.Calls) > 0 {
					t.Fatal("unexpected resolver")
				}
				if upToDate && r.ResultOID != before {
					t.Fatal("up-to-date changed")
				}
				if !upToDate && policy == "no-ff" && testutil.Must(t, g, "rev-parse", "HEAD^1") != before {
					t.Fatal("no-ff ignored")
				}
			})
		}
	}
}
func TestNativeFailuresNeverMasqueradeAsConflicts(t *testing.T) {
	for _, kind := range []string{"ff-only", "hook", "signing", "empty-pick"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "main"}
			switch kind {
			case "ff-only":
				q.MergeFF = "ff-only"
			case "hook":
				testutil.Write(t, g, ".git/hooks/pre-merge-commit", "#!/bin/sh\nexit 19\n")
				if e := os.Chmod(filepath.Join(g.Dir, ".git/hooks/pre-merge-commit"), 0700); e != nil {
					t.Fatal(e)
				}
				testutil.Must(t, g, "checkout", "-b", "clean", "main~1")
				testutil.Commit(t, g, "other", "other", "other")
			case "signing":
				q.Commit.Signing.Policy = options.Enabled
				testutil.Must(t, g, "config", "gpg.program", "/does-not-exist/git-ez-gpg")
			case "empty-pick":
				q.Operation = "cherry-pick"
				q.Target = "HEAD"
			}
			r, e := newEngine(t, g, f).Run(context.Background(), q)
			if e == nil || r.Integrated || r.Status != "failed" {
				t.Fatal(r, e)
			}
			if kind != "signing" && len(f.Calls) > 0 {
				t.Fatal("failed nonconflict invoked resolver")
			}
		})
	}
}
func TestGuardLowConfidenceAndUnsafeProposals(t *testing.T) {
	for _, kind := range []string{"low-confidence", "markers", "binary", "target", "branch", "index", "symlink", "oversized", "supporting-binary", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			base := f.Decision
			r := resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
				d, e := f.Resolve(ctx, s)
				if e != nil {
					return d, e
				}
				switch kind {
				case "low-confidence":
					d.Confidence = .1
				case "markers":
					testutil.Write(t, g, "file.txt", "<<<<<<< unresolved\n")
				case "binary":
					testutil.Write(t, g, "file.txt", "a\x00b")
				case "target":
					testutil.Must(t, g, "update-ref", "refs/heads/main", s.BeforeOID)
				case "branch":
					testutil.Must(t, g, "symbolic-ref", "HEAD", "refs/heads/main")
				case "index":
					testutil.Must(t, g, "add", "file.txt")
				case "symlink":
					if e = os.Remove(filepath.Join(g.Dir, "file.txt")); e != nil {
						t.Fatal(e)
					}
					if e = os.Symlink("elsewhere", filepath.Join(g.Dir, "file.txt")); e != nil {
						t.Fatal(e)
					}
				case "oversized":
					testutil.Write(t, g, "file.txt", strings.Repeat("x", 131073))
				case "supporting-binary":
					testutil.Write(t, g, "new.dat", "\x00")
				case "metadata":
					testutil.Write(t, g, ".git/MERGE_HEAD", s.BeforeOID)
				}
				return d, nil
			})
			_ = base
			result, e := newEngine(t, g, r).Run(context.Background(), engine.Request{Operation: "merge", Target: "main"})
			if e == nil || result.Integrated || result.ResolverCalls != 1 {
				t.Fatal(result, e)
			}
			if kind != "index" {
				stages, e := g.Conflicts(context.Background())
				if e != nil || len(stages) == 0 {
					t.Fatal("unsafe proposal staged", e)
				}
			}
		})
	}
}
