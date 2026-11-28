// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package engine_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/engine"
	"github.com/nico97118/git-ez-go/internal/process"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/terminal"
)

func TestInteractiveSIGINTHelper(t *testing.T) {
	ready := os.Getenv("GIT_EZ_PHASE5_SIGNAL_READY")
	if ready == "" {
		t.Skip("self-process helper")
	}
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	g, f := fixture(t)
	phase := os.Getenv("GIT_EZ_PHASE5_SIGNAL_PHASE")
	en := newEngine(t, g, resolverFunc(func(ctx context.Context, stop resolver.Stop) (resolver.Decision, error) {
		d, e := f.Resolve(ctx, stop)
		if e != nil {
			return d, e
		}
		if phase != "resolver" {
			return d, e
		}
		_, e = process.Run(ctx, process.Request{Args: []string{"/bin/sh", "-c", "printf ready > \"$1\"; sleep 0.4; printf late > file.txt", "helper", ready}, Dir: g.Dir})
		return d, e
	}))
	en.Out = io.Discard
	en.Report = terminal.New(io.Discard, terminal.Default, "")
	q := engine.Request{Operation: "merge", Target: "main", Interactive: true}
	if phase == "check" {
		q.Validation = []string{"/bin/sh", "-c", "printf ready > \"$1\"; sleep 0.4; printf late > file.txt", "helper", ready}
	}
	if phase == "review" {
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		en.In = &reviewReader{reader: reader, action: func() {
			if e := os.WriteFile(ready, []byte("ready"), 0600); e != nil {
				t.Fatal(e)
			}
		}}
	}
	result, e := en.Run(ctx, q)
	if !errors.Is(e, context.Canceled) || result.Integrated {
		t.Fatal(result, e)
	}
	time.Sleep(450 * time.Millisecond)
	raw, e := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
	if e != nil || !bytes.Contains(raw, []byte("<<<<<<<")) {
		t.Fatal("late child write after restoration", string(raw), e)
	}
}
func TestRepeatedSIGINTWaitsAndRestores(t *testing.T) {
	for _, phase := range []string{"resolver", "check", "review"} {
		t.Run(phase, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			binary, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(binary, "-test.run=^TestInteractiveSIGINTHelper$", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), "GIT_EZ_PHASE5_SIGNAL_READY="+ready, "GIT_EZ_PHASE5_SIGNAL_PHASE="+phase)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { _ = cmd.Process.Kill() }()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, e = os.Stat(ready); e == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("signal fixture did not start", output.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			for i := 0; i < 3; i++ {
				if e = cmd.Process.Signal(syscall.SIGINT); e != nil {
					t.Fatal(e)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e = cmd.Wait(); e != nil {
				t.Fatal(e, output.String())
			}
		})
	}
}
