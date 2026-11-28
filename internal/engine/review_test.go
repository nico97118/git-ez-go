// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/locking"
	"github.com/nico97118/git-ez-go/internal/process"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/terminal"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestGlobalInteractiveProposalActions(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		for _, choice := range []string{"accept", "stop", "eof", "revise", "low-confidence", "resolver-error"} {
			t.Run(op+"/"+choice, func(t *testing.T) {
				g, f := fixture(t)
				initial := ""
				calls := 0
				rr := resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
					calls++
					content, e := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
					if e != nil {
						t.Fatal(e)
					}
					if calls == 1 {
						initial = string(content)
					} else if string(content) != initial {
						t.Fatal("revision did not restore original conflict")
					}
					if stop.Review == nil || stop.Review.Round != calls {
						t.Fatal("missing authoritative review mission")
					}
					if calls > 1 && (stop.Review.Feedback != "keep both" || len(stop.Review.Previous) != calls-1) {
						t.Fatal("lost user feedback/history", stop.Review)
					}
					// The checkout remains locked throughout the resolver and every review.
					lockCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
					defer cancel()
					if lock, e := locking.Acquire(lockCtx, g); e == nil {
						lock.Close()
						t.Fatal("checkout lock lost")
					}
					decision, e := f.Resolve(ctx, stop)
					if choice == "low-confidence" {
						decision.Confidence = .01
					}
					if choice == "resolver-error" {
						return decision, errors.New("fixture resolver failure")
					}
					return decision, e
				})
				en := newEngine(t, g, rr)
				var out bytes.Buffer
				en.Out = &out
				answers := "a\n"
				if choice == "stop" {
					answers = "q\n"
				}
				if choice == "eof" {
					answers = ""
				}
				if choice == "revise" {
					answers = "r\nkeep both\nr\nkeep both\na\n"
				}
				en.In = strings.NewReader(answers)
				r, e := en.Run(context.Background(), engine.Request{Operation: op, Target: "main", Interactive: true})
				success := choice == "accept" || choice == "revise"
				if success != (e == nil && r.Integrated) {
					t.Fatal(r, e)
				}
				if !success {
					content, _ := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
					if string(content) != initial || testutil.Must(t, g, "ls-files", "--unmerged") == "" {
						t.Fatal("original native conflict not restored")
					}
				}
				reviews := view(t, r)["reviews"].([]any)
				want := 1
				if choice == "revise" {
					want = 3
				}
				if len(reviews) != want {
					t.Fatal("separate proposal evidence lost", len(reviews))
				}
				for _, item := range reviews {
					record := item.(map[string]any)
					if _, e := os.Stat(filepath.Join(r.Directory, record["artifacts"].(string), "decision.json")); e != nil {
						t.Fatal(e)
					}
				}
				if choice == "low-confidence" && reviews[0].(map[string]any)["decision"] != "accept" {
					t.Fatal("confidence bypass test did not reach explicit approval")
				}
				lock, e := locking.Acquire(context.Background(), g)
				if e != nil {
					t.Fatal(e)
				}
				lock.Close()
			})
		}
	}
}

type reviewReader struct {
	action func()
	reader io.Reader
	called bool
}

func (r *reviewReader) Read(p []byte) (int, error) {
	if !r.called {
		r.called = true
		r.action()
	}
	return r.reader.Read(p)
}
func TestReviewRejectsConcurrentMutationAndRestores(t *testing.T) {
	for _, kind := range []string{"content", "index", "mode", "metadata", "new-file", "new-symlink", "evidence", "target-ref"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			en := newEngine(t, g, f)
			en.Out = io.Discard
			en.In = &reviewReader{reader: strings.NewReader("a\n"), action: func() {
				switch kind {
				case "content":
					testutil.Write(t, g, "file.txt", "concurrent\n")
				case "index":
					testutil.Must(t, g, "add", "file.txt")
				case "mode":
					if e := os.Chmod(filepath.Join(g.Dir, "file.txt"), 0755); e != nil {
						t.Fatal(e)
					}
				case "metadata":
					testutil.Write(t, g, ".git/MERGE_MSG", "tampered")
				case "new-file":
					testutil.Write(t, g, "new-file", "concurrent supporting edit")
				case "new-symlink":
					if e := os.Symlink("file.txt", filepath.Join(g.Dir, "new-link")); e != nil {
						t.Fatal(e)
					}
				case "target-ref":
					testutil.Must(t, g, "update-ref", "refs/heads/main", testutil.Must(t, g, "rev-parse", "main~1"))
				case "evidence":
					paths, e := filepath.Glob(filepath.Join(g.Dir, ".git", "ez", "runs", "*", "proposal-*", "files.json"))
					if e != nil || len(paths) != 1 {
						t.Fatal(paths, e)
					}
					if e = os.WriteFile(paths[0], []byte("[]"), 0600); e != nil {
						t.Fatal(e)
					}
				}
			}}
			r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
			if e == nil || r.Integrated {
				t.Fatal(r, e)
			}
			if testutil.Must(t, g, "ls-files", "--unmerged") == "" {
				t.Fatal("staged mutated proposal")
			}
			if !strings.Contains(view(t, r)["reviews"].([]any)[0].(map[string]any)["restoration"].(string), "restored") {
				t.Fatal("restoration evidence missing")
			}
		})
	}
}
func TestInteractiveValidationRetryAndFreshFeedbackBudget(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "corrected", false: "fresh-budget"}[success], func(t *testing.T) {
			g, f := fixture(t)
			calls := 0
			marked := ""
			feedback := []string{}
			en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
				calls++
				raw, _ := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
				if calls == 1 {
					marked = string(raw)
				} else if string(raw) != marked {
					t.Fatal("retry did not restore baseline")
				}
				feedback = append(feedback, stop.Review.Feedback)
				d, e := f.Resolve(ctx, stop)
				text := "bad\n"
				if success && calls == 2 {
					text = "good\n"
				}
				testutil.Write(t, g, "file.txt", text)
				return d, e
			}))
			var presentation bytes.Buffer
			en.Err = &presentation
			en.Report = terminal.New(&presentation, terminal.Default, "")
			en.Config.ValidationAttempts = 2
			key := "synthetic-secret"
			en.Config.APIKey = &key
			en.Out = io.Discard
			input := "a\n"
			if !success {
				input = "a\nr\nretain compatibility\nq\n"
			}
			en.In = strings.NewReader(input)
			command := []string{"/bin/sh", "-c", "printf 'diagnostic synthetic-secret'; grep -qx good file.txt"}
			r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true, Validation: command})
			if success {
				if e != nil || !r.Integrated || calls != 2 {
					t.Fatal(r, e, calls)
				}
			} else {
				if e == nil || r.Integrated || calls != 4 {
					t.Fatal(r, e, calls)
				}
			}
			if !strings.Contains(presentation.String(), "Check in progress") || !strings.Contains(presentation.String(), "Validation failed") || strings.Contains(presentation.String(), key) {
				t.Fatal(presentation.String())
			}
			if !success && (strings.Contains(presentation.String(), "[a] Accept") || !strings.Contains(presentation.String(), "cannot be accepted")) {
				t.Fatal(presentation.String())
			}
			if !strings.Contains(feedback[1], "diagnostic <redacted>") || strings.Contains(strings.Join(feedback, ""), key) {
				t.Fatal("lost or unfiltered validation feedback", feedback)
			}
			if !success && feedback[2] != "retain compatibility" {
				t.Fatal("feedback budget not reset", feedback)
			}
			for _, item := range view(t, r)["reviews"].([]any) {
				record := item.(map[string]any)
				if record["decision"] == "accept" && record["validation"].(map[string]any)["status"] != "passed" {
					t.Fatal("accepted failed check")
				}
				for _, stream := range []string{"stdout", "stderr"} {
					if _, e := os.Stat(filepath.Join(r.Directory, record["artifacts"].(string), "validation."+stream)); e != nil {
						t.Fatal(e)
					}
				}
			}
		})
	}
}
func TestInteractiveCancellationWaitsForChildrenBeforeRestoration(t *testing.T) {
	g, f := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
		d, e := f.Resolve(ctx, stop)
		if e != nil {
			return d, e
		}
		close(started)
		_, e = process.Run(ctx, process.Request{Args: []string{"/bin/sh", "-c", "sleep 0.2; printf late > file.txt"}, Dir: g.Dir})
		return d, e
	}))
	en.Out = io.Discard
	go func() { <-started; time.Sleep(30 * time.Millisecond); cancel() }()
	r, e := en.Run(ctx, engine.Request{Operation: "merge", Target: "main", Interactive: true})
	if !errors.Is(e, context.Canceled) || r.Integrated {
		t.Fatal(r, e)
	}
	time.Sleep(250 * time.Millisecond)
	raw, _ := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if !bytes.Contains(raw, []byte("<<<<<<<")) {
		t.Fatal("late child corrupted restored conflict", string(raw))
	}
}
func TestInteractiveCleanupUncertaintyRetainsSnapshotWithoutRestoration(t *testing.T) {
	g, f := fixture(t)
	head := testutil.Must(t, g, "rev-parse", "HEAD")
	var index string
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
		index = testutil.Must(t, g, "ls-files", "--stage")
		d, _ := f.Resolve(ctx, stop)
		if err := os.Chmod(g.Dir+"/file.txt", 0755); err != nil {
			t.Fatal(err)
		}
		return d, failure.New(failure.Cleanup, "fixture uncertain group")
	}))
	en.Out = io.Discard
	r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
	if e == nil || !strings.Contains(e.Error(), "Backup retained at ") {
		t.Fatal(r, e)
	}
	raw, _ := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if string(raw) != "combined\n" {
		t.Fatal("restored despite uncertain child cleanup")
	}
	info, err := os.Stat(g.Dir + "/file.txt")
	if err != nil || info.Mode().Perm() != 0755 || index == "" || testutil.Must(t, g, "ls-files", "--stage") != index || testutil.Must(t, g, "rev-parse", "HEAD") != head {
		t.Fatal("unsafe restoration or staging after cleanup uncertainty", err)
	}
	backup := strings.Split(strings.Split(e.Error(), "Backup retained at ")[1], "\n")[0]
	t.Cleanup(func() { _ = os.RemoveAll(backup) })
	if _, err := os.Stat(filepath.Join(backup, "index")); err != nil {
		t.Fatal("snapshot lost", err)
	}
}

func TestInteractiveStagesHintedPathsAndRejectsWrongExecutableMode(t *testing.T) {
	for _, kind := range []string{"assume-unchanged", "skip-worktree", "mode-mismatch", "clean-filter"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			testutil.Commit(t, g, "support.txt", "old\n", "support")
			if kind == "assume-unchanged" || kind == "skip-worktree" {
				testutil.Must(t, g, "update-index", "--"+kind, "support.txt")
			}
			if kind == "mode-mismatch" {
				testutil.Must(t, g, "config", "core.fileMode", "false")
			}
			if kind == "clean-filter" {
				testutil.Write(t, g, ".git/info/attributes", "support.txt filter=upper\n")
				testutil.Must(t, g, "config", "filter.upper.clean", "tr a-z A-Z")
				testutil.Must(t, g, "config", "filter.upper.smudge", "cat")
				testutil.Must(t, g, "add", "--renormalize", "support.txt")
				testutil.Must(t, g, "commit", "-m", "normalize support")
			}
			en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
				d, e := f.Resolve(ctx, stop)
				testutil.Write(t, g, "support.txt", "new\n")
				if kind == "mode-mismatch" {
					if e = os.Chmod(filepath.Join(g.Dir, "file.txt"), 0755); e != nil {
						t.Fatal(e)
					}
				}
				return d, e
			}))
			en.Out = io.Discard
			en.In = strings.NewReader("a\n")
			r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
			if kind == "mode-mismatch" {
				if e == nil || r.Integrated || testutil.Must(t, g, "ls-files", "--unmerged") == "" {
					t.Fatal(r, e)
				}
				return
			}
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			want := "new"
			if kind == "clean-filter" {
				want = "NEW"
			}
			if got := testutil.Must(t, g, "show", "HEAD:support.txt"); got != want {
				t.Fatal("approved support edit not staged", got)
			}
			if kind == "assume-unchanged" || kind == "skip-worktree" {
				tag := testutil.Must(t, g, "ls-files", "-v", "--", "support.txt")
				if (kind == "assume-unchanged" && !strings.HasPrefix(tag, "h ")) || (kind == "skip-worktree" && !strings.HasPrefix(tag, "S ")) {
					t.Fatal("index hint lost", tag)
				}
			}
		})
	}
}

func TestInteractiveMultipleRebaseStopsAndGlobalFiles(t *testing.T) {
	for _, kind := range []string{"multiple-stops", "global-files"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			if kind == "multiple-stops" {
				testutil.Commit(t, g, "file.txt", "feature second\n", "second")
			} else {
				testutil.Must(t, g, "checkout", "main")
				testutil.Commit(t, g, "other.txt", "base\n", "other base")
				testutil.Must(t, g, "checkout", "feature")
				testutil.Commit(t, g, "other.txt", "feature other\n", "other feature")
				testutil.Must(t, g, "checkout", "main")
				testutil.Commit(t, g, "other.txt", "main other\n", "other main")
				testutil.Must(t, g, "checkout", "feature")
				combined := "other combined\n"
				f.Edits["other.txt"] = &combined
				support := "supporting\n"
				f.Edits["support.txt"] = &support
			}
			en := newEngine(t, g, f)
			en.Out = io.Discard
			en.In = strings.NewReader("r\nrevise first\na\na\n")
			q := engine.Request{Operation: "rebase", Target: "main", Interactive: true}
			if kind == "global-files" {
				q.Operation = "merge"
				en.In = strings.NewReader("a\n")
			}
			r, e := en.Run(context.Background(), q)
			if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			reviews := view(t, r)["reviews"].([]any)
			if kind == "multiple-stops" {
				if len(reviews) != 3 {
					t.Fatal("review rounds lost", reviews)
				}
			} else {
				if len(reviews) != 1 || len(reviews[0].(map[string]any)["paths"].([]any)) != 3 {
					t.Fatal("proposal was not globally reviewed", reviews)
				}
			}
		})
	}
}

func TestInteractiveRestorationFailureRetainsBackupAndNontrackedBlocker(t *testing.T) {
	g, f := fixture(t)
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
		d, e := f.Resolve(ctx, stop)
		if e != nil {
			return d, e
		}
		path := filepath.Join(g.Dir, "file.txt")
		if e = os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if e = os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(path, "notes"), []byte("preserve"), 0600); e != nil {
			t.Fatal(e)
		}
		return d, nil
	}))
	en.Out = io.Discard
	en.In = strings.NewReader("q\n")
	r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
	if e == nil || r.Integrated || !strings.Contains(e.Error(), "Conflict restoration failed; backup retained at ") {
		t.Fatal(r, e)
	}
	raw, err := os.ReadFile(filepath.Join(g.Dir, "file.txt", "notes"))
	if err != nil || string(raw) != "preserve" {
		t.Fatal("nontracked blocker lost", err)
	}
	part := strings.Split(e.Error(), "backup retained at ")[1]
	backup := strings.Split(part, ": ")[0]
	t.Cleanup(func() { _ = os.RemoveAll(backup) })
	if _, err = os.Stat(filepath.Join(backup, "index")); err != nil {
		t.Fatal("backup lost", err)
	}
}

func TestQuitRestoresNativeConflictForManualContinuation(t *testing.T) {
	for _, op := range []string{"merge", "rebase", "cherry-pick"} {
		t.Run(op, func(t *testing.T) {
			g, f := fixture(t)
			en := newEngine(t, g, f)
			en.In = strings.NewReader("q\n")
			en.Out = io.Discard
			r, e := en.Run(context.Background(), engine.Request{Operation: op, Target: "main", Interactive: true})
			if e == nil || r.Integrated {
				t.Fatal(r, e)
			}
			testutil.Write(t, g, "file.txt", "manual\n")
			testutil.Must(t, g, "add", "file.txt")
			continued, e := en.Run(context.Background(), engine.Request{Operation: op, Continue: true})
			if e != nil || !continued.Integrated {
				t.Fatal(continued, e)
			}
			if testutil.Must(t, g, "status", "--porcelain") != "" {
				t.Fatal("manual continuation left dirty state")
			}
		})
	}
}

func TestModelFreeOperationsDoNotCreatePersistentAudit(t *testing.T) {
	for _, kind := range []string{"up-to-date", "fast-forward", "native-validation-failure"} {
		t.Run(kind, func(t *testing.T) {
			g, f := fixture(t)
			q := engine.Request{Operation: "merge", Target: "HEAD"}
			if kind == "fast-forward" {
				testutil.Must(t, g, "checkout", "-b", "clean", "main~1")
				testutil.Commit(t, g, "other", "clean\n", "clean")
				q.Target = "main"
			}
			if kind == "native-validation-failure" {
				q.Validation = []string{"sh", "-c", "exit 17"}
			}
			r, e := newEngine(t, g, f).Run(context.Background(), q)
			if kind == "native-validation-failure" {
				if e == nil || !r.Integrated {
					t.Fatal(r, e)
				}
			} else if e != nil || !r.Integrated {
				t.Fatal(r, e)
			}
			if r.Directory != "" || r.ResolverCalls != 0 {
				t.Fatal("model-free run created audit", r)
			}
			if entries, err := os.ReadDir(filepath.Join(g.Dir, ".git", "ez", "runs")); err != nil || len(entries) != 0 {
				t.Fatal("persistent audit run created", entries, err)
			}
		})
	}
}

func TestAuditAttemptDecisionsAndApplicationRemainDistinct(t *testing.T) {
	for _, choice := range []string{"accept", "quit", "revise", "failure"} {
		t.Run(choice, func(t *testing.T) {
			g, f := fixture(t)
			rr := resolver.Resolver(f)
			if choice == "failure" {
				rr = resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
					d, _ := f.Resolve(ctx, stop)
					testutil.Write(t, g, "support.txt", "failed supporting change\n")
					return d, errors.New("scripted failure")
				})
			}
			en := newEngine(t, g, rr)
			en.Out = io.Discard
			input := "a\n"
			if choice == "quit" {
				input = "q\n"
			}
			if choice == "revise" {
				input = "r\nkeep intents\na\n"
			}
			en.In = strings.NewReader(input)
			r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
			success := choice == "accept" || choice == "revise"
			if success != (e == nil && r.Integrated) {
				t.Fatal(r, e)
			}
			records := view(t, r)["reviews"].([]any)
			for _, item := range records {
				record := item.(map[string]any)
				if record["stop_id"] == nil || record["conflict_ids"] == nil {
					t.Fatal("attempt identity missing", record)
				}
				if _, e = os.Stat(filepath.Join(r.Directory, record["artifacts"].(string), "files.json")); e != nil {
					t.Fatal("attempt not independently inspectable", e)
				}
			}
			first := records[0].(map[string]any)
			if choice == "revise" && (first["decision"] != "revise" || first["application"] != "not_staged") {
				t.Fatal("rejection confused with application", first)
			}
		})
	}
}

func TestLargeValidationCaptureIsCompleteAndAttributed(t *testing.T) {
	g, f := fixture(t)
	en := newEngine(t, g, f)
	en.Out = io.Discard
	en.In = strings.NewReader("a\n")
	q := engine.Request{Operation: "merge", Target: "main", Interactive: true, Validation: []string{"sh", "-c", "head -c 1500000 /dev/zero"}}
	r, e := en.Run(context.Background(), q)
	if e != nil || !r.Integrated {
		t.Fatal(r, e)
	}
	v := view(t, r)
	validation := v["validation"].(map[string]any)
	if validation["stdout_truncated"] != false {
		t.Fatal("validation capture incomplete", validation)
	}
	attempt := v["reviews"].([]any)[0].(map[string]any)
	if attempt["validation"].(map[string]any)["stdout_truncated"] != false {
		t.Fatal("attempt validation not attributable")
	}
	for _, path := range []string{filepath.Join(r.Directory, "validation.stdout"), filepath.Join(r.Directory, attempt["artifacts"].(string), "validation.stdout")} {
		info, e := os.Stat(path)
		if e != nil || info.Size() != 1500000 {
			t.Fatal("incomplete validation evidence", info, e)
		}
	}
}

func TestNativeFailureExitCodeDoesNotDependOnAuditDirectory(t *testing.T) {
	g, f := fixture(t)
	r, e := newEngine(t, g, f).Run(context.Background(), engine.Request{Operation: "merge", Target: "main", MergeFF: "ff-only"})
	if e == nil || failure.ExitCode(e) != 1 || r.Integrated || r.Status != "failed" || r.Directory != "" || r.ResolverCalls != 0 {
		t.Fatal("native failure was misclassified as admission failure", r, e, failure.ExitCode(e))
	}
}

func TestLargeProposalDiffIsCompleteAndApprovable(t *testing.T) {
	g, f := fixture(t)
	large := strings.Repeat("proposal-line\n", 100000)
	f.Edits["file.txt"] = &large
	en := newEngine(t, g, f)
	en.Config.MaxFileBytes = 2_000_000
	en.In = strings.NewReader("a\n")
	en.Out = io.Discard
	r, e := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true})
	if e != nil || !r.Integrated {
		t.Fatal(r, e)
	}
	record := view(t, r)["reviews"].([]any)[0].(map[string]any)
	info, e := os.Stat(filepath.Join(r.Directory, record["artifacts"].(string), "worktree.diff"))
	if e != nil || info.Size() < 1_000_000 {
		t.Fatal("large proposal evidence lost", info, e)
	}
}

func TestInterruptedAttemptRetainsSupportingProposalAndDiff(t *testing.T) {
	g, f := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
		d, e := f.Resolve(ctx, stop)
		if e != nil {
			return d, e
		}
		testutil.Write(t, g, "support.txt", "interrupted supporting edit\n")
		cancel()
		return d, context.Canceled
	}))
	en.Out = io.Discard
	r, e := en.Run(ctx, engine.Request{Operation: "merge", Target: "main", Interactive: true})
	if !errors.Is(e, context.Canceled) || r.Integrated {
		t.Fatal(r, e)
	}
	record := view(t, r)["reviews"].([]any)[0].(map[string]any)
	directory := filepath.Join(r.Directory, record["artifacts"].(string))
	raw, e := os.ReadFile(filepath.Join(directory, "files.json"))
	if e != nil || !bytes.Contains(raw, []byte("support.txt")) {
		t.Fatal("supporting evidence lost on cancellation", string(raw), e)
	}
	if _, e = os.Stat(filepath.Join(directory, "worktree.diff")); e != nil {
		t.Fatal("interrupted diff missing", e)
	}
}
