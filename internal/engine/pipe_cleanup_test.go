// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestUncertainPipeHelper(t *testing.T) {
	switch os.Getenv("EZ_PIPE_HELPER") {
	case "hold":
		fmt.Print("ready\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=TestUncertainPipeHelper$")
		child.Env = testutil.SetEnv(os.Environ(), "EZ_PIPE_HELPER", "hold")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(os.Getenv("EZ_PIPE_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	default:
		t.Skip("subprocess helper")
	}
}
func TestInheritedValidationPipeUncertaintyNeverRestoresOrRetries(t *testing.T) {
	g, f := fixture(t)
	marker := filepath.Join(t.TempDir(), "escaped-pid")
	g.Env = testutil.SetEnv(g.Env, "EZ_PIPE_HELPER", "spawn")
	g.Env = testutil.SetEnv(g.Env, "EZ_PIPE_PID", marker)
	defer func() {
		raw, err := os.ReadFile(marker)
		if err == nil {
			pid, err := strconv.Atoi(string(raw))
			if err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}()
	en := newEngine(t, g, f)
	en.In = strings.NewReader("a\n")
	en.Out = io.Discard
	result, err := en.Run(context.Background(), engine.Request{Operation: "merge", Target: "main", Interactive: true, Validation: []string{os.Args[0], "-test.run=TestUncertainPipeHelper$"}})
	if !failure.IsKind(err, failure.Cleanup) || !strings.Contains(err.Error(), "conflict was not restored") || result.Integrated || len(f.Calls) != 1 {
		t.Fatal(result, err, len(f.Calls))
	}
	raw, readErr := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if readErr != nil || string(raw) != "combined\n" {
		t.Fatal("restored while child remained alive", string(raw), readErr)
	}
	data := view(t, result)
	attempt := data["reviews"].([]any)[0].(map[string]any)
	if attempt["restoration"] != nil || data["validation"].(map[string]any)["stdout_truncated"] != true {
		t.Fatal("false restoration/complete-output evidence", attempt, data["validation"])
	}
	pidRaw, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, readErr := strconv.Atoi(string(pidRaw))
	if readErr != nil || syscall.Kill(pid, 0) != nil {
		t.Fatal("fixture did not retain a live descendant", readErr)
	}
	parts := strings.Split(err.Error(), "Backup retained at ")
	if len(parts) != 2 {
		t.Fatal(err)
	}
	backup := parts[1]
	t.Cleanup(func() { os.RemoveAll(backup) })
	if _, readErr = os.Stat(filepath.Join(backup, "index")); readErr != nil {
		t.Fatal("safety backup unavailable", readErr)
	}
}
