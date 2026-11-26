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
		case "descendant", "orphan":
			child := exec.Command(os.Args[0], "__process_helper", "latewrite", os.Args[3])
			if e := child.Start(); e != nil {
				os.Exit(3)
			}
			fmt.Print("ready\n")
			if os.Args[2] == "descendant" {
				_ = child.Wait()
				time.Sleep(time.Minute)
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
	r, e := process.Run(context.Background(), process.Request{Args: helper("sleep"), Timeout: 100 * time.Millisecond})
	var timeout *process.TimeoutError
	if !errors.As(e, &timeout) || !r.TimedOut || !strings.Contains(string(r.Stdout), "ready") {
		t.Fatalf("timeout %+v %v", r, e)
	}
	for _, mode := range []string{"descendant", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			sentinel := filepath.Join(t.TempDir(), "late")
			request := process.Request{Args: helper(mode, sentinel)}
			if mode == "descendant" {
				request.Timeout = 100 * time.Millisecond
			}
			r, e := process.Run(context.Background(), request)
			if mode == "descendant" {
				if !errors.As(e, &timeout) {
					t.Fatalf("cleanup uncertainty %v; cause: %v", e, errors.Unwrap(e))
				}
			} else if e != nil || r.ExitCode != 0 {
				t.Fatalf("result=%+v error=%v cause=%v", r, e, errors.Unwrap(e))
			}
			time.Sleep(350 * time.Millisecond)
			if _, e = os.Stat(sentinel); !os.IsNotExist(e) {
				t.Fatal("descendant mutated after return")
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
