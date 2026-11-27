// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package process_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nico97118/git-ez-go/internal/failure"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/process"
)

func TestMain(m *testing.M) {
	// Race runtime otherwise delays helper exit by one second, allowing the
	// deliberately orphaned writer to run before the parent has actually exited.
	_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
	if len(os.Args) > 2 && os.Args[1] == "__process_helper" {
		switch os.Args[2] {
		case "argv0":
			fmt.Print(os.Args[0])
		case "fd":
			f := os.NewFile(3, "inherited")
			b, e := io.ReadAll(f)
			if e != nil {
				os.Exit(5)
			}
			_, _ = os.Stdout.Write(b)
		case "argv":
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[3:])
			fmt.Fprint(os.Stderr, os.Getenv("EZ_PROCESS_TEST"))
		case "stdio":
			b, _ := io.ReadAll(os.Stdin)
			_, _ = os.Stdout.Write(b)
			fmt.Fprint(os.Stderr, "diagnostic")
		case "exit":
			os.Exit(23)
		case "sleep":
			fmt.Print("ready\n")
			time.Sleep(time.Minute)
		case "latewrite":
			time.Sleep(300 * time.Millisecond)
			_ = os.WriteFile(os.Args[3], []byte("late mutation"), 0600)
		case "escape-pipe":
			child := exec.Command(os.Args[0], "__process_helper", "sleep")
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if e := child.Start(); e != nil {
				os.Exit(3)
			}
			if e := os.WriteFile(os.Args[3], []byte(strconv.Itoa(child.Process.Pid)), 0600); e != nil {
				os.Exit(3)
			}
		case "ready":
			fmt.Print("ready\n")
			ready := os.NewFile(3, "ready")
			fmt.Fprintln(ready, 0)
			var gate [1]byte
			_, _ = io.ReadFull(os.Stdin, gate[:])
		case "gated-write":
			var gate [1]byte
			if _, err := io.ReadFull(os.Stdin, gate[:]); err == nil {
				_ = os.WriteFile(os.Args[3], []byte("late mutation"), 0600)
			}
		case "descendant", "orphan":
			child := exec.Command(os.Args[0], "__process_helper", "gated-write", os.Args[3])
			child.Stdin = os.Stdin
			if e := child.Start(); e != nil {
				os.Exit(3)
			}
			fmt.Print("ready\n")
			ready := os.NewFile(3, "ready")
			fmt.Fprintln(ready, child.Process.Pid)
			if os.Args[2] == "descendant" {
				_ = child.Wait()
			}

		default:
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func helper(mode string, args ...string) []string {
	return append([]string{os.Args[0], "__process_helper", mode}, args...)
}
func TestLiteralArgvAndSeparateStreams(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "must-not-exist")
	args := []string{"-S", "--", "with spaces", "$(touch " + sentinel + ")", "; touch " + sentinel}
	r, e := process.Run(context.Background(), process.Request{Args: helper("argv", args...), Env: []string{"EZ_PROCESS_TEST=expected"}})
	if e != nil || r.ExitCode != 0 || string(r.Stderr) != "expected" {
		t.Fatalf("run %+v %v", r, e)
	}
	var actual []string
	if json.Unmarshal(r.Stdout, &actual) != nil || strings.Join(actual, "|") != strings.Join(args, "|") {
		t.Fatal("literal argv changed")
	}
	if _, e = os.Stat(sentinel); !os.IsNotExist(e) {
		t.Fatal("shell interpretation occurred")
	}
	r, e = process.Run(context.Background(), process.Request{Args: helper("stdio"), Stdin: strings.NewReader("input\n")})
	if e != nil || string(r.Stdout) != "input\n" || string(r.Stderr) != "diagnostic" {
		t.Fatal(r, e)
	}
}
func TestExitAndStartupFailures(t *testing.T) {
	r, e := process.Run(context.Background(), process.Request{Args: helper("exit")})
	if e != nil || r.ExitCode != 23 || !r.Started {
		t.Fatal(r, e)
	}
	if _, e = process.Run(context.Background(), process.Request{Args: []string{"/does-not-exist/git-ez-test"}}); e == nil {
		t.Fatal("missing executable succeeded")
	}
	if _, e = process.Run(context.Background(), process.Request{}); e == nil {
		t.Fatal("empty argv accepted")
	}
}
func TestTimeoutRetainsOutputAndWaitsForChildren(t *testing.T) {
	const limit = 100 * time.Millisecond
	r, e := process.Run(context.Background(), process.Request{Args: helper("sleep"), Timeout: limit})
	var timeout *process.TimeoutError
	// The timeout includes startup; it may expire before the helper runs any Go.
	if !errors.As(e, &timeout) || !r.TimedOut || timeout.Limit != limit {
		t.Fatalf("timeout %+v %v", r, e)
	}
	for _, mode := range []string{"ready", "descendant", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			sentinel := filepath.Join(t.TempDir(), "late")
			readyRead, readyWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer readyRead.Close()
			defer readyWrite.Close()
			gateRead, gateWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer gateRead.Close()
			defer gateWrite.Close()
			// A watchdog bounds broken tests; cancellation under test happens
			// only after stdout was written and any descendant was started.
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			type outcome struct {
				result process.Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := process.Run(ctx, process.Request{
					Args: helper(mode, sentinel), Stdin: gateRead,
					ExtraFiles: []*os.File{readyWrite},
				})
				done <- outcome{result, err}
			}()
			announced := make(chan error, 1)
			go func() { var pid int; _, err := fmt.Fscan(readyRead, &pid); announced <- err }()
			select {
			case err := <-announced:
				if err != nil {
					cancel()
					result := <-done
					t.Fatalf("helper readiness: %v; result=%+v", err, result)
				}
			case <-ctx.Done():
				result := <-done
				t.Fatalf("helper did not announce readiness: %+v", result)
			}

			if mode != "orphan" {
				cancel()
			}
			result := <-done
			// Drop our copies so an open read end after Run would necessarily
			// belong to a managed process, not the test itself.
			if err := readyWrite.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gateRead.Close(); err != nil {
				t.Fatal(err)
			}
			if mode != "orphan" {
				if !errors.Is(result.err, context.Canceled) || result.result.TimedOut {
					t.Fatalf("cancellation: %+v", result)
				}
			} else if result.err != nil || result.result.ExitCode != 0 {
				t.Fatalf("orphan cleanup: %+v", result)
			}
			if !strings.Contains(string(result.result.Stdout), "ready") {
				t.Fatalf("ready output was not retained: %+v", result)
			}
			// A surviving descendant would receive this byte and mutate the
			// sentinel. EPIPE proves all inherited readers are already closed;
			// no fixed post-return sleep or scheduling window is needed.
			if _, err := gateWrite.Write([]byte{1}); !errors.Is(err, syscall.EPIPE) {
				t.Fatalf("managed stdin reader survived Run: %v", err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatal("managed process mutated after return", err)
			}
		})
	}
}

func TestCancellationIncludingStartupWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := process.Run(ctx, process.Request{Args: helper("exit")}); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled command started")
	}
	for i := 0; i < 8; i++ {
		sentinel := filepath.Join(t.TempDir(), "late")
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(time.Millisecond, cancel)
		_, e := process.Run(ctx, process.Request{Args: helper("latewrite", sentinel)})
		timer.Stop()
		cancel()
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancellation: %v", e)
		}
		if _, e = os.Stat(sentinel); !os.IsNotExist(e) {
			t.Fatal("late startup escaped ownership")
		}
	}
}
func TestForegroundPreservesNativeIOAndStatus(t *testing.T) {
	r, e := process.Run(context.Background(), process.Request{Args: helper("exit"), Foreground: true})
	if e != nil || r.ExitCode != 23 || !r.Started {
		t.Fatal(r, e)
	}
}

func TestExplicitDescriptorsRemainCallerOwned(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "descriptor")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	_, _ = f.WriteString("private input")
	_, _ = f.Seek(0, 0)
	r, e := process.Run(context.Background(), process.Request{Args: helper("fd"), ExtraFiles: []*os.File{f}})
	if e != nil || string(r.Stdout) != "private input" {
		t.Fatal(r, e)
	}
	if _, e = f.Stat(); e != nil {
		t.Fatal("caller descriptor was closed", e)
	}
}

func TestChildPATHAndWorkingDirectoryAreAuthoritative(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if e := os.Mkdir(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(os.Args[0], filepath.Join(bin, "foundation-helper")); e != nil {
		t.Fatal(e)
	}
	r, e := process.Run(context.Background(), process.Request{Args: []string{"foundation-helper", "__process_helper", "argv0"}, Dir: root, Env: []string{"PATH=bin"}})
	if e != nil || string(r.Stdout) != "foundation-helper" {
		t.Fatalf("child PATH/cwd/argv0 lost: %+v %v", r, e)
	}
}

func TestInheritedOutputPipeTimeoutIsCleanupUncertainty(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "escaped-pid")
	defer func() {
		raw, e := os.ReadFile(marker)
		if e == nil {
			pid, e := strconv.Atoi(string(raw))
			if e == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}()
	_, err := process.Run(context.Background(), process.Request{Args: helper("escape-pipe", marker)})
	if !failure.IsKind(err, failure.Cleanup) || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatal("inherited pipe uncertainty misclassified", err)
	}
}

func TestPATHSymlinkParentUsesPhysicalExecutable(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	lexical := filepath.Join(root, "lexical")
	for _, p := range []string{physical + "/child", lexical} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(physical+"/child", lexical+"/link"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ dir, text string }{{physical, "intended"}, {lexical, "wrong"}} {
		if err := os.WriteFile(item.dir+"/probe", []byte("#!/bin/sh\nprintf "+item.text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{lexical + "/link/..", "lexical/link/.."} {
		r, err := process.Run(context.Background(), process.Request{Args: []string{"probe"}, Dir: root, Env: []string{"PATH=" + path}})
		if err != nil || r.ExitCode != 0 || string(r.Stdout) != "intended" {
			t.Fatal(r, err)
		}
	}
}
