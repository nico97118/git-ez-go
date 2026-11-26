// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nico97118/git-ez-go/internal/options"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

// Exercise the reference's conflict shapes through the entire engine, including
// the meanings of absent stages and deletion of the obsolete rename source.
func TestNativeConflictShapesAndDeletion(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, shape := range []string{"add-add", "modify-delete", "rename-rename", "delete-proposal"} {
			t.Run(op+"/"+shape, func(t *testing.T) {
				g, _ := fixture(t)
				switch shape {
				case "add-add":
					for _, branch := range []string{"feature", "main"} {
						testutil.Must(t, g, "checkout", branch)
						testutil.Write(t, g, "added.txt", branch+"\n")
						testutil.Must(t, g, "add", "added.txt")
						testutil.Must(t, g, "commit", "--amend", "--no-edit")
					}
					testutil.Must(t, g, "checkout", "feature")
				case "modify-delete":
					testutil.Must(t, g, "rm", "file.txt")
					testutil.Must(t, g, "commit", "--amend", "-m", "delete feature")
				case "rename-rename":
					base := testutil.Must(t, g, "rev-parse", "main~1")
					for _, branch := range []string{"feature", "main"} {
						testutil.Must(t, g, "checkout", "-B", branch, base)
						testutil.Must(t, g, "mv", "file.txt", branch+".txt")
						testutil.Must(t, g, "commit", "-m", "rename "+branch)
					}
					testutil.Must(t, g, "checkout", "feature")
				}
				calls := 0
				r := resolverFunc(func(_ context.Context, s resolver.Stop) (resolver.Decision, error) {
					calls++
					if shape == "add-add" {
						found := false
						for _, c := range s.Conflicts {
							if c.Path == "added.txt" {
								found = true
								if c.Base != nil {
									t.Fatal("add/add has a base")
								}
							}
						}
						if !found {
							t.Fatal("missing add/add conflict")
						}
						testutil.Write(t, g, "added.txt", "combined\n")
						testutil.Write(t, g, "file.txt", "combined\n")
					} else if shape == "modify-delete" {
						if len(s.Conflicts) != 1 {
							t.Fatal(s.Conflicts)
						}
						c := s.Conflicts[0]
						if c.Base == nil || *c.Base != "base\n" {
							t.Fatal("base", c)
						}
						if op == "rebase" {
							if c.Ours == nil || *c.Ours != "main\n" || c.Theirs != nil {
								t.Fatal("rebase stages", c)
							}
						} else if c.Ours != nil || c.Theirs == nil || *c.Theirs != "main\n" {
							t.Fatal("stages", c)
						}
						if e := os.Remove(filepath.Join(g.Dir, "file.txt")); e != nil && !os.IsNotExist(e) {
							return resolver.Decision{}, e
						}
					} else if shape == "rename-rename" {
						paths := map[string]bool{}
						for _, c := range s.Conflicts {
							paths[c.Path] = true
						}
						if len(paths) != 3 || !paths["file.txt"] || !paths["main.txt"] || !paths["feature.txt"] {
							t.Fatal(paths)
						}
						for _, p := range []string{"file.txt", "feature.txt"} {
							if e := os.Remove(filepath.Join(g.Dir, p)); e != nil && !os.IsNotExist(e) {
								return resolver.Decision{}, e
							}
						}
						testutil.Write(t, g, "main.txt", "base\n")
					} else {
						if e := os.Remove(filepath.Join(g.Dir, "file.txt")); e != nil {
							return resolver.Decision{}, e
						}
					}
					return resolver.Decision{Rationale: "Preserve the intended conflict shape", Confidence: .95}, nil
				})
				result, err := newEngine(t, g, r).Run(context.Background(), engine.Request{Operation: op, Target: "main"})
				if op == "cherry-pick" && shape == "modify-delete" {
					if err == nil || result.Integrated || calls != 1 {
						t.Fatal("empty native pick must remain manual", result, err)
					}
					if _, err = os.Stat(filepath.Join(g.Dir, ".git", "CHERRY_PICK_HEAD")); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(result, err)
				}
				if calls != 1 || !result.Integrated {
					t.Fatal(result, calls)
				}
				if shape == "rename-rename" && testutil.Must(t, g, "ls-tree", "--name-only", "HEAD") != "main.txt" {
					t.Fatal("obsolete rename path resurrected")
				}
				if shape == "modify-delete" || shape == "delete-proposal" {
					if _, e := os.Stat(filepath.Join(g.Dir, "file.txt")); !os.IsNotExist(e) {
						t.Fatal("deletion not applied", e)
					}
				}
				if testutil.Must(t, g, "status", "--porcelain") != "" {
					t.Fatal("dirty result")
				}
			})
		}
	}
}

func TestUntouchedNativeResourcesAreNotAgentTextProposals(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, kind := range []string{"binary", "oversized"} {
			for _, side := range []string{"main", "feature"} {
				for _, interactive := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/%t", op, kind, side, interactive), func(t *testing.T) {
						g, f := fixture(t)
						testutil.Must(t, g, "checkout", side)
						content := "native\x00\xff"
						if kind == "oversized" {
							content = strings.Repeat("x", 131073)
						}
						testutil.Write(t, g, "resource.dat", content)
						testutil.Must(t, g, "add", "resource.dat")
						testutil.Must(t, g, "commit", "--amend", "--no-edit")
						testutil.Must(t, g, "checkout", "feature")
						e := newEngine(t, g, f)
						e.In = strings.NewReader("a\n")
						e.Out = io.Discard
						run(t, e, engine.Request{Operation: op, Target: "main", Interactive: interactive})
						raw, err := os.ReadFile(filepath.Join(g.Dir, "resource.dat"))
						if err != nil || string(raw) != content || len(f.Calls) != 1 {
							t.Fatal("native resource damaged", err)
						}
					})
				}
			}
		}
	}
}

func TestValidationStartupFailureIsNotRecordedAsSuccessfulExecution(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		t.Run(fmt.Sprint(interactive), func(t *testing.T) {
			g, f := fixture(t)
			e := newEngine(t, g, f)
			e.Out = io.Discard
			e.In = strings.NewReader("q\n")
			r, err := e.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: interactive, Validation: []string{"/does-not-exist/git-ez-validation"}})
			if err == nil || r.Integrated == interactive {
				t.Fatal(r, err)
			}
			data := view(t, r)
			v := data["validation"].(map[string]any)
			if v["status"] != "failed" || v["returncode"] != nil || v["stdout_available"] != false || v["stderr_available"] != false {
				t.Fatal("invented executable output/status", v)
			}
			for _, name := range []string{"validation.stdout", "validation.stderr"} {
				if _, err = os.Stat(filepath.Join(r.Directory, name)); !os.IsNotExist(err) {
					t.Fatal("invented capture", name, err)
				}
			}
		})
	}
}

func TestUnsupportedNativeConflictsStayManualWithoutResolverOrAudit(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, kind := range []string{"binary", "non-utf8", "symlink", "oversized"} {
			t.Run(op+"/"+kind, func(t *testing.T) {
				g, f := fixture(t)
				content := "feature\x00binary\n"
				switch kind {
				case "non-utf8":
					content = "feature\xff\n"
				case "oversized":
					content = strings.Repeat("x", 10000001)
				case "symlink":
					if err := os.Remove(filepath.Join(g.Dir, "file.txt")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("/does-not-exist/git-ez-resource", filepath.Join(g.Dir, "file.txt")); err != nil {
						t.Fatal(err)
					}
				}
				if kind != "symlink" {
					testutil.Write(t, g, "file.txt", content)
				}
				testutil.Must(t, g, "add", "file.txt")
				testutil.Must(t, g, "commit", "--amend", "--no-edit")
				before := testutil.Must(t, g, "rev-parse", "HEAD")
				r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: op, Target: "main"})
				if err == nil || r.Integrated || len(f.Calls) != 0 || r.Directory != "" {
					t.Fatal("unsupported conflict admitted", r, err, len(f.Calls))
				}
				active, err := g.IntegrationActive(context.Background(), op)
				if err != nil || !active {
					t.Fatal("native recovery lost", active, err)
				}
				if testutil.Must(t, g, "rev-parse", "feature") != before {
					t.Fatal("moved source branch")
				}
			})
		}
	}
}

func TestEngineOptionAdmissionRejectsBeforeMutation(t *testing.T) {
	message := "message"
	onto := "main"
	cases := []engine.Request{
		{Operation: "merge", Target: "main", MergeFF: "invalid"},
		{Operation: "rebase", Target: "main", MergeFF: "ff"},
		{Operation: "merge", Target: "main", Onto: &onto},
		{Operation: "rebase", Target: "main", Onto: &onto, Rebase: options.Rebase{KeepBase: options.Enabled}},
		{Operation: "merge", Target: "main", Rebase: options.Rebase{Empty: "keep"}},
		{Operation: "rebase", Target: "main", Commit: options.Commit{Message: &message}},
		{Operation: "rebase", Target: "main", Commit: options.Commit{Log: big.NewInt(1)}},
		{Operation: "rebase", Target: "main", Commit: options.Commit{Cleanup: "strip"}},
		{Operation: "rebase", Target: "main", Commit: options.Commit{VerifySignatures: options.Enabled}},
		{Operation: "merge", Continue: true, Target: "main"},
		{Operation: "rebase", Continue: true, Onto: &onto},
		{Operation: "rebase", Continue: true, Rebase: options.Rebase{Autosquash: options.Enabled}},
		{Operation: "merge", Continue: true, Commit: options.Commit{Signoff: options.Enabled}},
		{Operation: "merge", Continue: true, MergeFF: "ff"},
		{Operation: "merge", Continue: true, Rerere: options.Disabled},
		{Operation: "rebase", Continue: true},
		{Operation: "merge", Continue: true},
		{Operation: "cherry-pick", Continue: true},
		{Operation: "cherry-pick", Target: "main..feature"},
		{Operation: "rebase", Target: "main", Onto: ptr("does-not-exist")},
	}
	for i, q := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			g, f := fixture(t)
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			index := testutil.Must(t, g, "ls-files", "--stage")
			refs := testutil.Must(t, g, "show-ref")
			r, err := newEngine(t, g, f).Run(context.Background(), q)
			if err == nil || r.Integrated || r.Directory != "" || len(f.Calls) != 0 {
				t.Fatal("invalid options admitted", q, r, err)
			}
			if testutil.Must(t, g, "rev-parse", "HEAD") != before || testutil.Must(t, g, "ls-files", "--stage") != index || testutil.Must(t, g, "show-ref") != refs || testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("mutated on admission failure")
			}
			if _, err = os.Stat(filepath.Join(g.Dir, ".git/ez")); !os.IsNotExist(err) {
				t.Fatal("premature journal", err)
			}
		})
	}
}
func ptr(value string) *string { return &value }

func TestNativeCherryPickSequenceRemainsManuallyOwned(t *testing.T) {
	g, f := fixture(t)
	native, err := g.Run(context.Background(), "cherry-pick", "main", "main~1")
	if err != nil || native.ExitCode == 0 {
		t.Fatal(native, err)
	}
	index := testutil.Must(t, g, "ls-files", "--stage")
	raw, err := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "cherry-pick", Continue: true})
	if err == nil || r.Integrated || len(f.Calls) != 0 || r.Directory != "" {
		t.Fatal("adopted native sequence", r, err)
	}
	after, err := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if err != nil || string(after) != string(raw) || testutil.Must(t, g, "ls-files", "--stage") != index {
		t.Fatal("manual edits changed", err)
	}
	if _, err = os.Stat(filepath.Join(g.Dir, ".git/sequencer")); err != nil {
		t.Fatal(err)
	}
}

func TestRerereOverridePersistsAcrossEveryNativeRebaseStop(t *testing.T) {
	for _, autoupdate := range []bool{false, true} {
		t.Run(fmt.Sprint(autoupdate), func(t *testing.T) {
			g, _ := fixture(t)
			testutil.Commit(t, g, "file.txt", "second\n", "second")
			original := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "config", "rerere.enabled", "true")
			native, err := g.Run(context.Background(), "rebase", "main")
			if err != nil || native.ExitCode == 0 {
				t.Fatal(native, err)
			}
			for i, text := range []string{"rr-one\n", "rr-two\n"} {
				testutil.Write(t, g, "file.txt", text)
				testutil.Must(t, g, "add", "file.txt")
				native, err = g.Run(context.Background(), "rebase", "--continue")
				if err != nil || (native.ExitCode == 0) != (i == 1) {
					t.Fatal(native, err)
				}
			}
			testutil.Must(t, g, "reset", "--hard", original)
			configured := "true"
			policy := options.Disabled
			if autoupdate {
				configured = "false"
				policy = options.Enabled
			}
			testutil.Must(t, g, "config", "rerere.autoupdate", configured)
			calls := 0
			r := resolverFunc(func(_ context.Context, s resolver.Stop) (resolver.Decision, error) {
				expected := []string{"rr-one\n", "rr-two\n"}[calls]
				calls++
				if len(s.Conflicts) != 1 || s.Conflicts[0].Marked == nil || *s.Conflicts[0].Marked != expected {
					t.Fatal("native rerere result not supplied", s.Conflicts)
				}
				testutil.Write(t, g, "file.txt", expected)
				return resolver.Decision{Rationale: "Preserve reused native content", Confidence: .95}, nil
			})
			result := run(t, newEngine(t, g, r), engine.Request{Operation: "rebase", Target: "main", Rerere: policy})
			expectedCalls := 2
			if autoupdate {
				expectedCalls = 0
			}
			if calls != expectedCalls || testutil.Must(t, g, "show", "HEAD:file.txt") != "rr-two" || testutil.Must(t, g, "config", "rerere.autoupdate") != configured || (autoupdate && result.Directory != "") {
				t.Fatal(result, calls)
			}
		})
	}
}

func TestRerereReuseDoesNotRetryRejectedCommitHooks(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			g, f := fixture(t)
			original := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "config", "rerere.enabled", "true")
			native, err := g.Run(context.Background(), "merge", "main")
			if err != nil || native.ExitCode == 0 {
				t.Fatal(native, err)
			}
			testutil.Write(t, g, "file.txt", "combined\n")
			testutil.Must(t, g, "add", "file.txt")
			testutil.Must(t, g, "merge", "--continue")
			testutil.Must(t, g, "reset", "--hard", original)
			testutil.Must(t, g, "config", "rerere.autoupdate", "true")
			if err = os.WriteFile(filepath.Join(g.Dir, ".git/hooks/pre-commit"), []byte("#!/bin/sh\nprintf rejected\\n >> .git/rejected-hook\nexit 23\n"), 0700); err != nil {
				t.Fatal(err)
			}
			policy := options.Unspecified
			if explicit {
				policy = options.Enabled
			}
			r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Rerere: policy})
			active, _ := g.Active(context.Background())
			raw, readErr := os.ReadFile(filepath.Join(g.Dir, ".git/rejected-hook"))
			if err == nil || r.Integrated || !active || len(f.Calls) != 0 || readErr != nil || strings.Count(string(raw), "rejected") != 1 || r.Directory != "" {
				t.Fatal(r, err, active, len(f.Calls), string(raw), readErr)
			}
		})
	}
}

func TestAuditDistinguishesAnEmptyFileFromADeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			g, f := fixture(t)
			empty := ""
			if deleted {
				f.Edits["file.txt"] = nil
			} else {
				f.Edits["file.txt"] = &empty
			}
			result := run(t, newEngine(t, g, f), engine.Request{Operation: "merge", Target: "main"})
			data := view(t, result)
			attempt := data["reviews"].([]any)[0].(map[string]any)
			if attempt["status"] != "applied" || attempt["application"] != "staged" {
				t.Fatal(attempt)
			}
			directory := filepath.Join(result.Directory, attempt["artifacts"].(string))
			raw, err := os.ReadFile(filepath.Join(directory, "files.json"))
			if err != nil {
				t.Fatal(err)
			}
			var files []map[string]any
			if err = json.Unmarshal(raw, &files); err != nil || len(files) != 1 {
				t.Fatal(string(raw), err)
			}
			record := files[0]
			if deleted {
				if record["state"] != "deleted" || record["artifact"] != nil {
					t.Fatal(record)
				}
				if _, err = os.Stat(filepath.Join(g.Dir, "file.txt")); !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else {
				if record["state"] != "file" || record["size"] != float64(0) {
					t.Fatal(record)
				}
				for _, path := range []string{filepath.Join(g.Dir, "file.txt"), filepath.Join(directory, record["artifact"].(string))} {
					content, err := os.ReadFile(path)
					if err != nil || len(content) != 0 {
						t.Fatal(path, err)
					}
				}
			}
		})
	}
}

func TestNativeBinaryResourceCannotBeHiddenByRestoringHEADOrChangingMode(t *testing.T) {
	for _, change := range []string{"restore-head", "mode"} {
		t.Run(change, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Write(t, g, "resource.dat", "old\x00resource")
			testutil.Must(t, g, "add", "resource.dat")
			testutil.Must(t, g, "commit", "-m", "shared resource")
			common := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "branch", "-f", "main", common)
			testutil.Commit(t, g, "file.txt", "feature-new\n", "feature text")
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			testutil.Must(t, g, "checkout", "main")
			testutil.Write(t, g, "file.txt", "target-new\n")
			testutil.Write(t, g, "resource.dat", "Git\x00resource")
			testutil.Must(t, g, "commit", "-am", "target text and resource")
			testutil.Must(t, g, "checkout", "feature")
			r := resolverFunc(func(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
				d, err := f.Resolve(ctx, s)
				if err != nil {
					return d, err
				}
				path := filepath.Join(g.Dir, "resource.dat")
				if change == "restore-head" {
					testutil.Write(t, g, "resource.dat", "old\x00resource")
				} else if err = os.Chmod(path, 0755); err != nil {
					return d, err
				}
				return d, nil
			})
			result, err := newEngine(t, g, r).Run(context.Background(), engine.Request{Operation: "merge", Target: "main"})
			active, _ := g.Active(context.Background())
			if err == nil || result.Integrated || !active || testutil.Must(t, g, "rev-parse", "HEAD") != before || !strings.Contains(err.Error(), "resource.dat") {
				t.Fatal("hidden native resource modification accepted", result, err)
			}
		})
	}
}
func TestNonUTF8ProposalFailsWithoutStagingOrUnfinishedAudit(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			bad := "invalid\xff\n"
			f.Edits["file.txt"] = &bad
			before := testutil.Must(t, g, "rev-parse", "HEAD")
			result, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: op, Target: "main"})
			if err == nil || result.Integrated || view(t, result)["status"] != "failed" || testutil.Must(t, g, "rev-parse", "feature") != before || testutil.Must(t, g, "ls-files", "--unmerged") == "" {
				t.Fatal(result, err)
			}
		})
	}
}

func TestOldGitIsRejectedBeforeNativeOperationChanges(t *testing.T) {
	g, f := fixture(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "git")
	script := "#!/bin/sh\nfor argument do if [ \"$argument\" = --version ]; then printf 'git version 2.38.5\\n'; exit 0; fi; done\nexec '" + strings.ReplaceAll(realGit, "'", "'\"'\"'") + "' \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	g.Env = testutil.SetEnv(g.Env, "PATH", dir+":"+os.Getenv("PATH"))
	before := testutil.Must(t, g, "rev-parse", "HEAD")
	index := testutil.Must(t, g, "ls-files", "--stage")
	r, err := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "merge", Target: "main"})
	if err == nil || !strings.Contains(err.Error(), "Git 2.46") || r.Directory != "" || len(f.Calls) != 0 || testutil.Must(t, g, "rev-parse", "HEAD") != before || testutil.Must(t, g, "ls-files", "--stage") != index {
		t.Fatal(r, err)
	}
}
