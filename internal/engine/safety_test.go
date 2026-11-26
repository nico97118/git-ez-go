// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/locking"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestCompleteEngineLockLifetimeAndJournalFailure(t *testing.T) {
	g, f := fixture(t)
	entered := make(chan resolver.Stop, 1)
	release := make(chan struct{})
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
		entered <- s
		select {
		case <-release:
			return f.Resolve(ctx, s)
		case <-ctx.Done():
			return resolver.Decision{}, ctx.Err()
		}
	}))
	done := make(chan error, 1)
	go func() {
		_, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Validation: []string{"sh", "-c", "sleep .2"}})
		done <- e
	}()
	var stop resolver.Stop
	select {
	case stop = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("resolver not reached")
	}
	if lock, e := locking.Acquire(context.Background(), g); e == nil {
		_ = lock.Close()
		t.Fatal("lock released during resolver")
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	lock, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	b, e := os.ReadFile(filepath.Join(stop.ArtifactDirectory, "run.json"))
	if e != nil || !strings.Contains(string(b), `"status": "succeeded"`) {
		t.Fatal("lock released before finalization", e)
	}
}
func TestRequiredJournalCapturePrecedesStaging(t *testing.T) {
	for _, kind := range []string{"unsafe-parent", "resolver-replacement", "finalization"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "main"}
			var rr resolver.Resolver = f
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			if kind == "unsafe-parent" {
				if e := os.Symlink(t.TempDir(), filepath.Join(g.Dir, ".git/ez")); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "resolver-replacement" {
				rr = resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
					d, e := f.Resolve(ctx, s)
					if er := os.Rename(s.ArtifactDirectory, s.ArtifactDirectory+"-retained"); er != nil {
						t.Fatal(er)
					}
					if er := os.Symlink(t.TempDir(), s.ArtifactDirectory); er != nil {
						t.Fatal(er)
					}
					return d, e
				})
			}
			if kind == "finalization" {
				q.Validation = []string{"sh", "-c", "chmod 500 .git/ez/runs/*"}
			}
			r, e := newEngine(t, g, rr).Run(context.Background(), q)
			if e == nil || r.Status == "succeeded" {
				t.Fatal(r, e)
			}
			if kind == "finalization" {
				if !r.Integrated {
					t.Fatal("native completion lost")
				}
				_ = os.Chmod(r.Directory, 0700)
			} else {
				if testutil.Must(t, g, "rev-parse", "HEAD") != before {
					t.Fatal("changed before journal safety")
				}
				if kind == "resolver-replacement" && testutil.Must(t, g, "ls-files", "--unmerged") == "" {
					t.Fatal("staged without evidence")
				}
			}
			lock, e := locking.Acquire(context.Background(), g)
			if e != nil {
				t.Fatal("failed operation retained lock", e)
			}
			_ = lock.Close()
		})
	}
}
func TestResolverCancellationAndConflictRoundBudget(t *testing.T) {
	for _, kind := range []string{"cancel", "round-limit", "interactive"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var rr resolver.Resolver = f
			q := engine.Request{Operation: "rebase", Target: "main"}
			if kind == "cancel" {
				rr = resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
					cancel()
					return f.Resolve(ctx, s)
				})
			}
			if kind == "round-limit" {
				testutil.Commit(t, g, "file.txt", "second\n", "second")
			}
			if kind == "interactive" {
				q.Interactive = true
			}
			en := newEngine(t, g, rr)
			en.Config.MaxRounds = 1
			en.In = strings.NewReader("")
			r, e := en.Run(ctx, q)
			if e == nil || r.Integrated {
				t.Fatal(r, e)
			}
			if kind == "cancel" && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			if kind == "cancel" {
				records := view(t, r)["reviews"].([]any)
				if len(records) != 1 || records[0].(map[string]any)["status"] != "interrupted" {
					t.Fatal("interrupted attempt was misclassified", records)
				}
			}
			if kind == "round-limit" && r.ResolverCalls != 1 {
				t.Fatal("round limit")
			}
			if kind == "interactive" && r.ResolverCalls != 1 {
				t.Fatal("interactive EOF must retain one proposal and restore")
			}
		})
	}
}
func TestNativeSigningAndAuthorPreservation(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			key := filepath.Join(t.TempDir(), "key")
			cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
			if b, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("ssh-keygen: %v %s", e, b)
			}
			pub, e := os.ReadFile(key + ".pub")
			if e != nil {
				t.Fatal(e)
			}
			allowed := filepath.Join(t.TempDir(), "allowed")
			if e = os.WriteFile(allowed, append([]byte("test@example.invalid "), pub...), 0600); e != nil {
				t.Fatal(e)
			}
			testutil.Must(t, g, "config", "gpg.format", "ssh")
			testutil.Must(t, g, "config", "user.signingkey", key)
			testutil.Must(t, g, "config", "gpg.ssh.allowedSignersFile", allowed)
			q := engine.Request{Operation: op, Target: "main", Commit: options.Commit{Signing: options.Signing{Policy: options.Enabled}}}
			run(t, newEngine(t, g, f), q)
			testutil.Must(t, g, "verify-commit", "HEAD")
			if op == "cherry-pick" && testutil.Must(t, g, "show", "-s", "--format=%an <%ae>%n%s", "HEAD") != testutil.Must(t, g, "show", "-s", "--format=%an <%ae>%n%s", "main") {
				t.Fatalf("author/message changed: result=%q target=%q", testutil.Must(t, g, "show", "-s", "--format=%an <%ae>%n%s", "HEAD"), testutil.Must(t, g, "show", "-s", "--format=%an <%ae>%n%s", "main"))
			}
		})
	}
}
func TestEngineInLinkedWorktreeAndUnrelatedRootPick(t *testing.T) {
	for _, kind := range []string{"linked", "root-pick"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "main"}
			if kind == "linked" {
				directory := filepath.Join(t.TempDir(), "worktree")
				testutil.Must(t, g, "worktree", "add", "-b", "linked", directory, "feature")
				g.Dir = directory
			} else {
				testutil.Must(t, g, "checkout", "--orphan", "unrelated")
				testutil.Must(t, g, "rm", "-rf", ".")
				oid := testutil.Commit(t, g, "root.txt", "root\n", "root")
				testutil.Must(t, g, "checkout", "feature")
				q.Operation = "cherry-pick"
				q.Target = oid
			}
			run(t, newEngine(t, g, f), q)
			if kind == "root-pick" && len(f.Calls) != 0 {
				t.Fatal("unrelated root invoked resolver")
			}
		})
	}
}

func TestFailedCommitHookCannotManufactureAnotherResolverStop(t *testing.T) {
	g, f := fixture(t)
	hook := filepath.Join(g.Dir, ".git/hooks/pre-commit")
	if e := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'called\\n' >> .git/hook-calls\ncp .git/saved-conflict-index .git/index\nexit 23\n"), 0700); e != nil {
		t.Fatal(e)
	}
	rr := resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
		raw, e := os.ReadFile(filepath.Join(g.Dir, ".git/index"))
		if e != nil {
			return resolver.Decision{}, e
		}
		if e = os.WriteFile(filepath.Join(g.Dir, ".git/saved-conflict-index"), raw, 0600); e != nil {
			return resolver.Decision{}, e
		}
		return f.Resolve(ctx, s)
	})
	en := newEngine(t, g, rr)
	en.Config.MaxRounds = 2
	result, e := en.Run(context.Background(), engine.Request{Operation: "cherry-pick", Target: "main"})
	if e == nil || result.Integrated || result.ResolverCalls != 1 {
		t.Fatal("failed hook was retried or treated as another replay stop", result, e)
	}
	calls, e := os.ReadFile(filepath.Join(g.Dir, ".git/hook-calls"))
	if e != nil || string(calls) != "called\n" {
		t.Fatal("hook invocation count", string(calls), e)
	}
}
