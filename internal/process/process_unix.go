//go:build linux || freebsd || darwin

// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package process owns subprocess lifetimes. It never interprets argv as shell
// text; callers explicitly request /bin/sh only for an agent shell tool.
package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nico97118/git-ez-go/internal/failure"
)

type Request struct {
	Args           []string
	Dir            string
	Env            []string // nil inherits the caller; an empty slice is an empty environment.
	Stdin          io.Reader
	Stdout, Stderr io.Writer     // nil captures the corresponding stream.
	Timeout        time.Duration // zero has no independent deadline; context still applies.
	Foreground     bool          // preserve native terminal/process-group ownership.
	ExtraFiles     []*os.File    // explicitly inherited files, starting at descriptor 3.
}
type Result struct {
	Started                          bool
	Stdout, Stderr                   []byte
	ExitCode                         int
	TimedOut                         bool
	StdoutTruncated, StderrTruncated bool
}
type TimeoutError struct{ Limit time.Duration }

func (e *TimeoutError) Error() string { return "Command timed out" }
func (e *TimeoutError) Unwrap() error { return context.DeadlineExceeded }

// Run always waits for the direct child. Managed commands additionally kill and
// confirm termination of the owned process group before returning. Uncertainty
// is a Cleanup failure: a caller must not restore files in that state.
func Run(ctx context.Context, r Request) (result Result, err error) {
	if len(r.Args) == 0 {
		return result, failure.New(failure.Usage, "An executable is required")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if r.Timeout < 0 {
		return result, failure.New(failure.Usage, "Command timeout must be nonnegative")
	}
	commandCtx := ctx
	cancel := func() {}
	if r.Timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, r.Timeout)
	}
	defer cancel()
	environment := r.Env
	if environment == nil {
		environment = os.Environ()
	}
	environment = append([]string{}, environment...)
	executable, e := executablePath(r.Args[0], r.Dir, environment)
	if e != nil {
		return result, failure.Wrap(failure.Execution, "Cannot locate executable", e)
	}
	cmd := exec.Command(executable, r.Args[1:]...)
	cmd.Args[0] = r.Args[0]
	cmd.Dir = r.Dir
	cmd.Env = environment
	cmd.Stdin = r.Stdin
	cmd.ExtraFiles = r.ExtraFiles
	if !r.Foreground {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = r.Stdout
	if cmd.Stdout == nil {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = r.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	// Bound pipe cleanup if a command leaves a descriptor in a different session.
	cmd.WaitDelay = 2 * time.Second
	if e := commandCtx.Err(); e != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.TimedOut = true
		return result, &TimeoutError{Limit: r.Timeout}
	}
	if err = cmd.Start(); err != nil {
		return result, failure.Wrap(failure.Execution, "Cannot start executable", err)
	}
	result.Started = true
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waited error
	select {
	case waited = <-done:
	case <-commandCtx.Done():
		var killErr error
		if r.Foreground {
			killErr = cmd.Process.Kill()
		} else {
			killErr = killGroup(cmd.Process.Pid)
		}
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = failure.Wrap(failure.Cleanup, "Cannot terminate child process", killErr)
			_ = cmd.Process.Kill()
		}
		select {
		case waited = <-done:
		case <-time.After(5 * time.Second):
			return result, failure.Wrap(failure.Cleanup, "Cannot confirm child process termination", err)
		}
	}
	result.Stdout = append([]byte(nil), stdout.Bytes()...)
	result.Stderr = append([]byte(nil), stderr.Bytes()...)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.ExitCode = -int(status.Signal())
		}
	}
	if !r.Foreground {
		cleanup := stopGroup(cmd.Process.Pid, 5*time.Second)
		if cleanup != nil {
			return result, failure.Wrap(failure.Cleanup, "Cannot confirm child process group termination", cleanup)
		}
		// A prior signal may have raced with process exit. Independently
		// confirmed group disappearance resolves that cleanup uncertainty.
		err = nil
	}
	if err != nil {
		return result, err
	}
	if errors.Is(waited, exec.ErrWaitDelay) {
		result.StdoutTruncated, result.StderrTruncated = true, true
		return result, failure.Wrap(failure.Cleanup, "Cannot confirm inherited output cleanup; a child may still be running", waited)
	}
	if commandCtx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return result, context.Canceled
		}
		result.TimedOut = true
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, context.DeadlineExceeded
		}
		return result, &TimeoutError{Limit: r.Timeout}
	}
	var exit *exec.ExitError
	if waited != nil && !errors.As(waited, &exit) {
		return result, failure.Wrap(failure.Execution, "Cannot collect child process output", waited)
	}
	return result, nil
}
func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func stopGroup(pid int, limit time.Duration) error {
	signalErr := killGroup(pid)
	if signalErr != nil && !errors.Is(signalErr, syscall.EPERM) {
		return signalErr
	}
	// Darwin can return EPERM while an already-killed group is being reaped.
	// Never infer termination from that error: wait for ESRCH. A live or
	// inaccessible group at the deadline still fails with Cleanup uncertainty.
	deadline := time.Now().Add(limit)
	for {
		probeErr := syscall.Kill(-pid, 0)
		if errors.Is(probeErr, syscall.ESRCH) {
			return nil
		}
		if probeErr != nil && !errors.Is(probeErr, syscall.EPERM) {
			return probeErr
		}
		if time.Now().After(deadline) {
			if signalErr != nil {
				return signalErr
			}
			if probeErr != nil {
				return probeErr
			}
			return errors.New("child process group remains present")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Match native/Python execvpe lookup using the supplied child PATH and cwd,
// including an explicitly configured relative PATH entry. exec.Command alone
// searches the parent's PATH and rejects relative matches via ErrDot.
func executablePath(name, dir string, env []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	search := "/bin:/usr/bin"
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "PATH" {
			search = value
		}
	}
	var denied error
	for _, base := range strings.Split(search, string(os.PathListSeparator)) {
		candidate := filepath.Join(base, name)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(dir, candidate)
			var e error
			candidate, e = filepath.Abs(candidate)
			if e != nil {
				return "", e
			}
		}
		path, e := exec.LookPath(candidate)
		if e == nil {
			return path, nil
		}
		if errors.Is(e, os.ErrPermission) {
			denied = e
		}
	}
	if denied != nil {
		return "", denied
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
