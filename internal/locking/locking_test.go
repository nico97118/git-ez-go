// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package locking_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/locking"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "__lock_helper" {
		g, e := git.New(os.Args[2])
		if e != nil {
			os.Exit(3)
		}
		l, e := locking.Acquire(context.Background(), g)
		if e != nil {
			os.Exit(2)
		}
		defer l.Close()
		fmt.Println("ready")
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
		return
	}
	os.Exit(m.Run())
}
func owner(t *testing.T, g git.Client) (*exec.Cmd, func()) {
	t.Helper()
	c := exec.Command(os.Args[0], "__lock_helper", g.Dir)
	c.Env = g.Env
	stdin, e := c.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := c.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Start(); e != nil {
		t.Fatal(e)
	}
	ready := make(chan bool, 1)
	go func() { s, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- s == "ready\n" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("owner did not lock")
		}
	case <-time.After(10 * time.Second):
		_ = c.Process.Kill()
		t.Fatal("owner barrier timed out")
	}
	stopped := false
	cleanup := func() {
		if !stopped {
			_, _ = stdin.Write([]byte{'\n'})
			_ = stdin.Close()
			_ = c.Wait()
			stopped = true
		}
	}
	t.Cleanup(cleanup)
	return c, cleanup
}
func TestInterprocessContentionAndProcessDeath(t *testing.T) {
	g := testutil.Conflicted(t)
	beforeHead := testutil.Must(t, g, "rev-parse", "HEAD")
	beforeIndex := testutil.Must(t, g, "ls-files", "--stage")
	c, release := owner(t, g)
	start := time.Now()
	if l, e := locking.Acquire(context.Background(), g); e == nil {
		l.Close()
		t.Fatal("concurrent owner accepted")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("lock acquisition blocked")
	}
	if e := c.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	release()
	l, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	if e = l.Close(); e != nil {
		t.Fatal("close not idempotent", e)
	}
	if testutil.Must(t, g, "rev-parse", "HEAD") != beforeHead || testutil.Must(t, g, "ls-files", "--stage") != beforeIndex {
		t.Fatal("owner death changed repository state")
	}
	if active, e := g.Active(context.Background()); e != nil || !active {
		t.Fatal("active native operation lost", e)
	}
	p := filepath.Join(g.Dir, ".git/ez-execution.lock")
	if info, e := os.Stat(p); e != nil || !info.Mode().IsRegular() {
		t.Fatal("lock inode deleted")
	}
}
func TestLinkedWorktreesHaveIndependentLocks(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Commit(t, g, "file", "base", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	testutil.Must(t, g, "worktree", "add", "-b", "linked", linked, "HEAD")
	_, release := owner(t, g)
	lg := git.Client{Dir: linked, Env: g.Env}
	l, e := locking.Acquire(context.Background(), lg)
	if e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
	release()
}
func TestUnsafeLockFilesAndStaleFileReuse(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			g := testutil.Repo(t)
			p := filepath.Join(g.Dir, ".git/ez-execution.lock")
			var e error
			switch kind {
			case "symlink":
				e = os.Symlink(filepath.Join(g.Dir, ".git/config"), p)
			case "directory":
				e = os.Mkdir(p, 0700)
			case "fifo":
				e = syscall.Mkfifo(p, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if l, e := locking.Acquire(context.Background(), g); e == nil {
				l.Close()
				t.Fatal("unsafe lock accepted")
			}
		})
	}
	g := testutil.Repo(t)
	p := filepath.Join(g.Dir, ".git/ez-execution.lock")
	_ = os.WriteFile(p, []byte("stale"), 0600)
	l, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal("file existence treated as active owner", e)
	}
	_ = l.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "stale" {
		t.Fatal("persistent inode contents rewritten")
	}
}
func TestLockCoversDeferredFinalizationAndReleasesOnFailure(t *testing.T) {
	g := testutil.Repo(t)
	sentinel := errors.New("validation failed")
	e := locking.With(context.Background(), g, func() error {
		defer func() {
			if l, e := locking.Acquire(context.Background(), g); e == nil {
				l.Close()
				t.Error("finalization outside lock")
			}
		}()
		return sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic suppressed")
			}
		}()
		_ = locking.With(context.Background(), g, func() error { panic("interruption") })
	}()
	l, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
}
func TestChildDoesNotRetainLockDescriptor(t *testing.T) {
	g := testutil.Repo(t)
	l, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	// A long-lived shell is a real exec child; it must not retain the lock inode.
	child := exec.Command("/bin/sh", "-c", "printf ready; read value")
	stdin, _ := child.StdinPipe()
	stdout, _ := child.StdoutPipe()
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = stdin.Write([]byte("done\n")); _ = stdin.Close(); _ = child.Wait() })
	b := make([]byte, 5)
	if _, e = stdout.Read(b); e != nil || string(b) != "ready" {
		t.Fatal("child not started")
	}
	_ = l.Close()
	next, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal("child inherited lock", e)
	}
	_ = next.Close()
}
func TestCancellationAndAcquisitionErrors(t *testing.T) {
	g := testutil.Repo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l, e := locking.Acquire(ctx, g); e == nil {
		l.Close()
		t.Fatal("cancelled lock admitted")
	}
	if e := os.Chmod(filepath.Join(g.Dir, ".git"), 0500); e != nil {
		t.Fatal(e)
	}
	_, e := locking.Acquire(context.Background(), g)
	_ = os.Chmod(filepath.Join(g.Dir, ".git"), 0700)
	if e == nil {
		t.Fatal("acquisition error ignored")
	}
	l, e := locking.Acquire(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
}
