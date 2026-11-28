// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/application"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestAssistedTerminalLevelsAndStreams(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, mode := range []string{"default", "--quiet", "--verbose"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				g := testutil.Conflicted(t)
				testutil.Must(t, g, "merge", "--abort")
				testutil.Must(t, g, "config", "ez.model", "fixture")
				testutil.Must(t, g, "config", "ez.apiKey", "presentation-secret")
				text := "combined\n"
				f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Preserve both intentions", Confidence: .95}}
				var out, diag bytes.Buffer
				args := []string{op, "feature", "--repo", g.Dir}
				if mode != "default" {
					args = append(args, mode)
				}
				code := cli.RunCLI(context.Background(), args, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{Resolver: f})
				if code != 0 || len(f.Calls) != 1 || strings.Contains(out.String(), "git-ez:") || strings.Contains(out.String(), "Status:") {
					t.Fatal(code, out.String(), diag.String())
				}
				s := diag.String()
				if mode == "--quiet" && strings.Contains(s, "Retained diagnostics") {
					t.Fatal(s)
				}
				if strings.Contains(s, "completed successfully.") != (mode != "--quiet") || strings.Contains(s, "Resolution attempt 1") != (mode != "--quiet") {
					t.Fatal(s)
				}
				if mode != "--quiet" {
					for _, want := range []string{"Model: fixture", "1 conflict set resolved (1 file occurrence)", "Resolution time:", "Audit:"} {
						if !strings.Contains(s, want) {
							t.Fatal(want, s)
						}
					}
					at := strings.LastIndex(s, "Audit: ")
					path := strings.TrimSpace(s[at+len("Audit: "):])
					if _, err := os.Stat(filepath.Join(path, "run.json")); err != nil {
						t.Fatal(err, s)
					}
				}
				if strings.Contains(s, "\x1b") || strings.Contains(s, "presentation-secret") {
					t.Fatal(s)
				}
			})
		}
	}
}

func TestInteractiveTerminalReviewIsOnStderrAndQuietStillReviews(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	for _, choice := range []string{"a\n", "r\npreserve both\na\n", "q\n"} {
		t.Run(strings.TrimSpace(choice), func(t *testing.T) {
			g := testutil.Conflicted(t)
			testutil.Must(t, g, "merge", "--abort")
			testutil.Must(t, g, "config", "ez.model", "fixture")
			text := "combined\n"
			f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Runtime behavior has not been verified.", Confidence: .95}}
			var out, diag bytes.Buffer
			code := cli.RunCLI(context.Background(), []string{"merge", "feature", "--repo", g.Dir, "--interactive", "--quiet"}, "test", cli.Streams{In: strings.NewReader(choice), Out: &out, Err: &diag}, application.Application{Resolver: f})
			if (code == 0) != (choice != "q\n") {
				t.Fatal(code, diag.String())
			}
			s := diag.String()
			for _, want := range []string{"Review proposed resolution", "Conflict set 1 | 1 file | Attempt 1", "Proposed diff:", "model-reported, not independently verified", "Validation (git-ez)", "Choice:"} {
				if !strings.Contains(s, want) || strings.Contains(out.String(), want) {
					t.Fatal(want, out.String(), s)
				}
			}
			if strings.Contains(s, "confidence") || strings.Contains(s, "Resolution time:") {
				t.Fatal(s)
			}
			if strings.HasPrefix(choice, "r") && (!strings.Contains(s, "Attempt 2") || len(f.Calls) != 2) {
				t.Fatal(s)
			}
			if choice == "q\n" && !strings.Contains(s, "git ez merge --continue") {
				t.Fatal(s)
			}
		})
	}
}

func TestTerminalFailuresReportActualGitState(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	for _, kind := range []string{"invalid-proposal", "final-check"} {
		t.Run(kind, func(t *testing.T) {
			g := testutil.Conflicted(t)
			testutil.Must(t, g, "merge", "--abort")
			testutil.Must(t, g, "config", "ez.model", "fixture")
			text := "combined\n"
			if kind == "invalid-proposal" {
				text = "<<<<<<< unresolved\n"
			}
			f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Unverified model claim", Confidence: .95}}
			args := []string{"merge", "feature", "--repo", g.Dir, "--quiet"}
			if kind == "final-check" {
				args = append(args, "--check", "sh", "-c", "printf retained-check-diagnostic >&2; exit 7")
			}
			var out, diag bytes.Buffer
			code := cli.RunCLI(context.Background(), args, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{Resolver: f})
			if code != 1 {
				t.Fatal(code, diag.String())
			}
			s := diag.String()
			active, err := g.IntegrationActive(context.Background(), "merge")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "invalid-proposal" {
				if !active || !strings.Contains(s, "Unable to resolve conflicts") || !strings.Contains(s, "merge is still in progress") || !strings.Contains(s, "git ez merge --continue --quiet") {
					t.Fatal(s)
				}
			} else {
				if active || strings.Contains(s, "git ez merge --continue") {
					t.Fatal(s)
				}
				for _, want := range []string{"Final validation failed.", "Git integration is complete.", "Exit code: 7", "retained-check-diagnostic", "Retained diagnostics (may be incomplete):"} {
					if !strings.Contains(s, want) {
						t.Fatal(want, s)
					}
				}
			}
			if !strings.Contains(s, "Audit:") || strings.Contains(out.String(), "Status:") {
				t.Fatal(out.String(), s)
			}
		})
	}
}

func TestNativeSuccessIsTransparentForEachOperation(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		t.Run(op, func(t *testing.T) {
			g := testutil.Repo(t)
			testutil.Commit(t, g, "base", "base\n", "base")
			testutil.Must(t, g, "checkout", "-b", "feature")
			testutil.Commit(t, g, "new", "new\n", "new")
			testutil.Must(t, g, "checkout", "main")
			testutil.Must(t, g, "config", "ez.model", "unused")
			f := &testutil.FakeResolver{}
			var out, diag bytes.Buffer
			code := cli.RunCLI(context.Background(), []string{op, "feature", "--repo", g.Dir}, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{Resolver: f})
			if code != 0 || len(f.Calls) != 0 || strings.Contains(out.String()+diag.String(), "git-ez:") || out.Len()+diag.Len() == 0 {
				t.Fatal(code, out.String(), diag.String())
			}
			if _, err := os.Stat(filepath.Join(g.Dir, ".git/ez/runs")); err == nil {
				entries, err := os.ReadDir(filepath.Join(g.Dir, ".git/ez/runs"))
				if err != nil || len(entries) != 0 {
					t.Fatal(entries, err)
				}
			}
		})
	}
}

func TestCheckCommandRedactsCredentialsBeforeQuoting(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	g := testutil.Repo(t)
	testutil.Commit(t, g, "base", "base\n", "base")
	testutil.Must(t, g, "config", "ez.model", "unused")
	secret := "credential'with spaces"
	testutil.Must(t, g, "config", "ez.apiKey", secret)
	var out, diag bytes.Buffer
	code := cli.RunCLI(context.Background(), []string{"merge", "HEAD", "--repo", g.Dir, "--verbose", "--check", "sh", "-c", `printf '%s' "$1" >&2; exit 7`, "sh", secret}, "test", cli.Streams{Out: &out, Err: &diag}, application.Application{})
	if code != 1 || strings.Contains(diag.String(), "credential") || !strings.Contains(diag.String(), "<redacted>") {
		t.Fatal(code, out.String(), diag.String())
	}
}
