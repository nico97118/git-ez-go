// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nico97118/git-ez-go/internal/application"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionDispatchUsesNativeConfigurationAndGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	g := testutil.Repo(t)
	testutil.Commit(t, g, "file", "base", "base")
	testutil.Must(t, g, "checkout", "-b", "feature")
	target := testutil.Commit(t, g, "other", "other", "other")
	testutil.Must(t, g, "checkout", "main")
	testutil.Must(t, g, "config", "ez.model", "deterministic")
	var out, err bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"merge", "feature", "--repo", g.Dir}, "test", cli.Streams{Out: &out, Err: &err}, application.Application{})
	if code != 0 || !bytes.Contains(out.Bytes(), []byte("Fast-forward")) || strings.Contains(err.String(), "git-ez:") || strings.Contains(out.String(), "Status:") || testutil.Must(t, g, "rev-parse", "HEAD") != target {
		t.Fatal(code, out.String(), err.String())
	}
}
func TestNativeContinuationDoesNotRequireModelConfiguration(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_EDITOR", "true")
	g := testutil.Conflicted(t)
	testutil.Write(t, g, "file.txt", "manual\n")
	testutil.Must(t, g, "add", "file.txt")
	var out, err bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"merge", "--continue", "--repo", g.Dir}, "test", cli.Streams{Out: &out, Err: &err}, application.Application{})
	if code != 0 {
		t.Fatal(code, out.String(), err.String())
	}
	if testutil.Must(t, g, "status", "--porcelain") != "" {
		t.Fatal("not completed")
	}
}
func TestUnavailableServicesAndMissingResolverFailExplicitly(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	g := testutil.Conflicted(t)
	testutil.Must(t, g, "merge", "--abort")
	testutil.Must(t, g, "config", "ez.model", "deterministic")
	var out, err bytes.Buffer
	c := config.Defaults()
	c.Model = "deterministic"
	en, e := engine.New(context.Background(), g, c, nil)
	if e != nil {
		t.Fatal(e)
	}
	result, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "feature"})
	if e == nil || result.Integrated || testutil.Must(t, g, "ls-files", "--unmerged") == "" {
		t.Fatal(result, e)
	}
	out.Reset()
	err.Reset()
	code := cli.RunCLI(context.Background(), []string{"audit", "list", "--repo", t.TempDir()}, "test", cli.Streams{Out: &out, Err: &err}, application.Application{})
	if code != 2 {
		t.Fatal(code)
	}
}

type interruptedResolver struct{}

func (interruptedResolver) Resolve(_ context.Context, s resolver.Stop) (resolver.Decision, error) {
	if err := os.WriteFile(filepath.Join(s.Worktree, "file.txt"), []byte("partial proposal\n"), 0600); err != nil {
		return resolver.Decision{}, err
	}
	return resolver.Decision{}, context.Canceled
}
func TestCLIInterruptedIntegrationPreservesReferenceExitAndRecoveryPolicy(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_EDITOR", "true")
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", op, interactive), func(t *testing.T) {
				g := testutil.Conflicted(t)
				testutil.Must(t, g, "merge", "--abort")
				testutil.Must(t, g, "config", "ez.model", "fixture")
				secret := "synthetic'credential with spaces"
				testutil.Must(t, g, "config", "ez.apiKey", secret)
				args := []string{op, "feature", "--repo", g.Dir}
				if interactive {
					args = append(args, "--interactive")
				}
				args = append(args, "--check", "validation-tool", "--token", secret)
				var out, diag bytes.Buffer
				code := cli.RunCLI(context.Background(), args, "test", cli.Streams{In: strings.NewReader(""), Out: &out, Err: &diag}, application.Application{Resolver: interruptedResolver{}})
				expected := 1
				if interactive {
					expected = 130
				}
				if code != expected || !strings.Contains(diag.String(), "git ez "+op+" --continue") || !strings.Contains(diag.String(), "git "+op+" --continue") || strings.Contains(diag.String(), "synthetic") || !strings.Contains(diag.String(), "<redacted>") {
					t.Fatal(code, out.String(), diag.String())
				}
				raw, err := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
				if err != nil {
					t.Fatal(err)
				}
				if interactive && !bytes.Contains(raw, []byte("<<<<<<<")) || !interactive && string(raw) != "partial proposal\n" {
					t.Fatal("wrong cancellation restoration", string(raw))
				}
			})
		}
	}
}

func TestInterruptedFinalValidationDoesNotRecommendContinuingCompletedGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_EDITOR", "true")
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			g := testutil.Conflicted(t)
			testutil.Must(t, g, "config", "ez.model", "fixture")
			text := "combined\n"
			f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Combine intentions", Confidence: .95}}
			args := []string{"merge"}
			if native {
				testutil.Write(t, g, "file.txt", "manual\n")
				testutil.Must(t, g, "add", "file.txt")
				args = append(args, "--continue")
			} else {
				testutil.Must(t, g, "merge", "--abort")
				args = append(args, "feature")
			}
			marker := filepath.Join(t.TempDir(), "validation-started")
			args = append(args, "--repo", g.Dir, "--check", "sh", "-c", `printf started > "$1"; sleep 20`, "sh", marker)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan bool, 1)
			go func() {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(marker); err == nil {
						cancel()
						done <- true
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				done <- false
			}()
			var out, diag bytes.Buffer
			code := cli.RunCLI(ctx, args, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{Resolver: f})
			expected := 1
			if native {
				expected = 130
			}
			if !<-done || code != expected || !strings.Contains(diag.String(), "Git integration is complete.") || !strings.Contains(diag.String(), "Final validation failed.") || strings.Contains(out.String(), "Git integration completed:") || strings.Contains(diag.String(), "git ez merge --continue") {
				t.Fatal(code, out.String(), diag.String())
			}
			active, err := g.Active(context.Background())
			if err != nil || active {
				t.Fatal(active, err)
			}
		})
	}
}

func TestModelFreeValidationFailureKeepsDiagnosticsWithoutPersistentAudit(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	g := testutil.Repo(t)
	testutil.Commit(t, g, "base", "base", "base")
	testutil.Must(t, g, "checkout", "-b", "feature")
	testutil.Commit(t, g, "new", "new", "new")
	testutil.Must(t, g, "checkout", "main")
	testutil.Must(t, g, "config", "ez.model", "unused")
	testutil.Must(t, g, "config", "ez.apiKey", "diagnostic-secret")
	var out, diag bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"merge", "feature", "--repo", g.Dir, "--check", "sh", "-c", "printf native-check-diagnostic; printf diagnostic-secret >&2; exit 7"}, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{})
	if code != 1 || !strings.Contains(diag.String(), "Git integration is complete.") || !strings.Contains(diag.String(), "Final validation failed.") || strings.Contains(out.String(), "Git integration completed:") || !strings.Contains(diag.String(), "native-check-diagnostic") || !strings.Contains(diag.String(), "<redacted>") || strings.Contains(diag.String(), "diagnostic-secret") {
		t.Fatal(code, out.String(), diag.String())
	}
	if entries, err := os.ReadDir(filepath.Join(g.Dir, ".git/ez/runs")); err != nil || len(entries) != 0 {
		t.Fatal("native validation created persistent LLM audit", err)
	}
}
