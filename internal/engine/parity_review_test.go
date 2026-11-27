// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/options"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func reviewSequenceEditor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sequence editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// A manually owned stop must be completed first. Only its subsequent amendment
// conflict is eligible for assistance; fixup -C/-c must retain the replacement title.
func TestAmendContinuationTransfersOnlyNewConflict(t *testing.T) {
	for _, kind := range []string{"native-C", "native-c", "assisted-C"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			g, f := fixture(t)
			original := testutil.Must(t, g, "rev-parse", "HEAD")
			base := testutil.Must(t, g, "rev-parse", "main")
			amended := testutil.Commit(t, g, "file.txt", "amended\n", "amend! feature\n\nreplacement title")
			en := newEngine(t, g, f)
			if kind == "assisted-C" {
				en.Resolver = resolverFunc(func(context.Context, resolver.Stop) (resolver.Decision, error) {
					return resolver.Decision{}, errors.New("intentional manual handoff")
				})
				result, err := en.Run(ctx, engine.Request{Operation: "rebase", Target: "main", Rebase: options.Rebase{Autosquash: options.Enabled}})
				if err == nil || result.Integrated || result.ResolverCalls != 1 {
					t.Fatal(result, err)
				}
			} else {
				editor := "true"
				if kind == "native-c" {
					editor = reviewSequenceEditor(t, "sed 's/fixup -C /fixup -c /g' \"$1\" > \"$1.next\"\nmv \"$1.next\" \"$1\"\n")
				}
				result, err := g.RunWithFiles(ctx, nil, editor, "rebase", "--interactive", "--autosquash", "main")
				if err != nil || result.ExitCode == 0 {
					t.Fatal(result, err)
				}
			}
			if testutil.Must(t, g, "rev-parse", "REBASE_HEAD") != original {
				t.Fatal("not at the original manually owned stop")
			}
			testutil.Write(t, g, "file.txt", "manual\n")
			testutil.Must(t, g, "add", "file.txt")
			en.Resolver = resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
				if testutil.Must(t, g, "rev-parse", "REBASE_HEAD") != amended || testutil.Must(t, g, "show", "HEAD:file.txt") != "manual" {
					t.Fatal("assisted the wrong replay stop")
				}
				if len(stop.Conflicts) != 1 || stop.Conflicts[0].Theirs == nil || *stop.Conflicts[0].Theirs != "amended\n" {
					t.Fatal(stop.Conflicts)
				}
				return f.Resolve(ctx, stop)
			})
			result, err := en.Run(ctx, engine.Request{Operation: "rebase", Continue: true})
			if err != nil || !result.Integrated || result.ResolverCalls != 1 || len(f.Calls) != 1 {
				t.Fatal(result, err, len(f.Calls))
			}
			if testutil.Must(t, g, "rev-parse", "HEAD^") != base || testutil.Must(t, g, "show", "-s", "--format=%B", "HEAD") != "replacement title" {
				t.Fatal("lost native amendment ancestry or message")
			}
			if testutil.Must(t, g, "show", "HEAD:file.txt") != "combined" || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("incorrect amendment tree/index")
			}
			if active, err := g.Active(ctx); err != nil || active {
				t.Fatal("operation did not finish", err)
			}
		})
	}
}

func TestContinuationExecFailureAfterProgressRemainsManual(t *testing.T) {
	ctx := context.Background()
	g, f := fixture(t)
	editor := reviewSequenceEditor(t, "printf '\\nexec printf unrelated-exec-failure >&2; false\\n' >> \"$1\"\n")
	result, err := g.RunWithFiles(ctx, nil, editor, "rebase", "--interactive", "main")
	if err != nil || result.ExitCode == 0 {
		t.Fatal(result, err)
	}
	testutil.Write(t, g, "file.txt", "manual\n")
	testutil.Must(t, g, "add", "file.txt")
	en := newEngine(t, g, f)
	var diagnostics bytes.Buffer
	en.Out = &diagnostics
	en.Err = &diagnostics
	continued, err := en.Run(ctx, engine.Request{Operation: "rebase", Continue: true})
	if err != nil || continued.NativeStatus == nil || *continued.NativeStatus == 0 || len(f.Calls) != 0 || continued.Directory != "" {
		t.Fatal(continued, err)
	}
	if !strings.Contains(diagnostics.String(), "unrelated-exec-failure") {
		t.Fatal("missing native exec diagnostic", diagnostics.String())
	}
	if active, err := g.Active(ctx); err != nil || !active {
		t.Fatal("native recovery state missing", err)
	}
	if stages, err := g.Conflicts(ctx); err != nil || len(stages) != 0 {
		t.Fatal(stages, err)
	}
	if testutil.Must(t, g, "show", "HEAD:file.txt") != "manual" || testutil.Must(t, g, "diff", "--name-only") != "" {
		t.Fatal("manual result changed")
	}
}

func TestFinalValidationCannotSwitchBranchAtSameOID(t *testing.T) {
	for _, operation := range []string{"merge", "rebase", "cherry-pick"} {
		t.Run(operation, func(t *testing.T) {
			g, f := fixture(t)
			en := newEngine(t, g, f)
			result, err := en.Run(context.Background(), engine.Request{Operation: operation, Target: "main", Validation: []string{"git", "switch", "-c", "validation-branch"}})
			if err == nil || !result.Integrated || result.Status != "failed" || result.ResolverCalls != 1 {
				t.Fatal(result, err)
			}
			if testutil.Must(t, g, "rev-parse", "HEAD") != result.ResultOID || testutil.Must(t, g, "symbolic-ref", "--short", "HEAD") != "validation-branch" {
				t.Fatal("did not exercise a branch change at the same commit")
			}
			data := view(t, result)
			validation := data["validation"].(map[string]any)
			if validation["status"] != "passed" || validation["returncode"] != float64(0) || data["status"] != "failed" {
				t.Fatal(data)
			}
			if active, err := g.Active(context.Background()); err != nil || active {
				t.Fatal("completed Git operation was restarted", err)
			}
			if testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("unexpected validation worktree/index mutation")
			}
		})
	}
}
