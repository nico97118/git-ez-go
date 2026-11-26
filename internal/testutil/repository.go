// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package testutil contains isolated native Git fixtures and deterministic
// resolvers. Nothing in production imports this package.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/resolver"
)

func Environment() []string {
	var env []string
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		if !strings.HasPrefix(k, "GIT_") && k != "LC_ALL" && k != "LANG" {
			env = append(env, s)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C", "LANG=C", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
}
func SetEnv(base []string, key, value string) []string {
	out := make([]string, 0, len(base)+1)
	for _, s := range base {
		if !strings.HasPrefix(s, key+"=") {
			out = append(out, s)
		}
	}
	return append(out, key+"="+value)
}
func Repo(t testing.TB) git.Client {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	g, e := git.New(root)
	if e != nil {
		t.Fatal(e)
	}
	g.Env = Environment()
	Must(t, g, "init", "-b", "main")
	Must(t, g, "config", "user.name", "Test")
	Must(t, g, "config", "user.email", "test@example.invalid")
	Must(t, g, "config", "commit.gpgSign", "false")
	return g
}
func Must(t testing.TB, g git.Client, args ...string) string {
	t.Helper()
	r, e := g.Checked(context.Background(), args...)
	if e != nil {
		t.Fatalf("Git %v: %v; stderr=%s", args, e, r.Stderr)
	}
	return strings.TrimSpace(string(r.Stdout))
}
func Write(t testing.TB, g git.Client, path, content string) {
	t.Helper()
	p := filepath.Join(g.Dir, path)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func Commit(t testing.TB, g git.Client, path, content, message string) string {
	t.Helper()
	Write(t, g, path, content)
	Must(t, g, "add", "--", path)
	Must(t, g, "commit", "-m", message)
	return Must(t, g, "rev-parse", "HEAD")
}
func Conflicted(t testing.TB) git.Client {
	t.Helper()
	g := Repo(t)
	Commit(t, g, "file.txt", "base\n", "base")
	Must(t, g, "checkout", "-b", "feature")
	Commit(t, g, "file.txt", "feature\n", "feature")
	Must(t, g, "checkout", "main")
	Commit(t, g, "file.txt", "main\n", "main")
	r, e := g.Run(context.Background(), "merge", "--no-edit", "feature")
	if e != nil || r.ExitCode == 0 {
		t.Fatalf("expected conflict: %+v %v", r, e)
	}
	return g
}

type FakeResolver struct {
	mu       sync.Mutex
	Calls    []resolver.Stop
	Edits    map[string]*string
	Decision resolver.Decision
	Failure  error
}

func (f *FakeResolver) Resolve(ctx context.Context, s resolver.Stop) (resolver.Decision, error) {
	if e := ctx.Err(); e != nil {
		return resolver.Decision{}, e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, s)
	g := git.Client{Dir: s.Worktree}
	for name, text := range f.Edits {
		p, e := g.SafePath(name)
		if e != nil {
			return resolver.Decision{}, e
		}
		if text == nil {
			if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
				return resolver.Decision{}, e
			}
		} else {
			if e = os.WriteFile(p, []byte(*text), 0600); e != nil {
				return resolver.Decision{}, e
			}
		}
	}
	return f.Decision, f.Failure
}

var _ resolver.Resolver = (*FakeResolver)(nil)
