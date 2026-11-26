// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package git_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestEnvironmentAndNativeForeground(t *testing.T) {
	g := testutil.Repo(t)
	other := testutil.Repo(t)
	for _, entry := range []string{"GIT_DIR=" + filepath.Join(other.Dir, ".git"), "GIT_WORK_TREE=" + other.Dir, "GIT_INDEX_FILE=" + filepath.Join(other.Dir, "index"), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=Caller identity", "GIT_AUTHOR_NAME=Author from environment", "GIT_AUTHOR_EMAIL=author@example.invalid", "GIT_EDITOR=custom-editor", "GIT_SEQUENCE_EDITOR=custom-sequence", "GIT_TERMINAL_PROMPT=1"} {
		g.Env = append(g.Env, entry)
	}
	if testutil.Must(t, g, "rev-parse", "--show-toplevel") != g.Dir || testutil.Must(t, g, "config", "user.name") != "Caller identity" || !strings.HasPrefix(testutil.Must(t, g, "var", "GIT_AUTHOR_IDENT"), "Author from environment <author@example.invalid>") {
		t.Fatal("environment redirected or lost caller config")
	}
	var out, err bytes.Buffer
	r, e := g.Foreground(context.Background(), nil, &out, &err, "var", "GIT_EDITOR")
	if e != nil || r.ExitCode != 0 || strings.TrimSpace(out.String()) != "custom-editor" {
		t.Fatal("native editor changed", e, out.String())
	}
	if testutil.Must(t, g, "var", "GIT_EDITOR") != "true" {
		t.Fatal("assisted editor override lost")
	}
	out.Reset()
	r, e = g.Foreground(context.Background(), nil, &out, &err, "ez-definitely-unknown")
	if e != nil || r.ExitCode != 1 || !strings.Contains(err.String(), "not a git command") {
		t.Fatalf("native error status lost: %+v %v %s", r, e, err.String())
	}
}
func TestRevisionPathsStagesAndBlobLimits(t *testing.T) {
	g := testutil.Conflicted(t)
	c, e := g.Conflicts(context.Background())
	if e != nil || len(c) != 1 || len(c["file.txt"]) != 3 {
		t.Fatal(c, e)
	}
	oid, e := g.CommitOID(context.Background(), "HEAD~0")
	if e != nil || oid != testutil.Must(t, g, "rev-parse", "HEAD") {
		t.Fatal(oid, e)
	}
	if _, e = g.CommitOID(context.Background(), "--help"); e == nil {
		t.Fatal("revision interpreted as option")
	}
	if _, e = g.BranchOID(context.Background(), "invalid branch"); e == nil {
		t.Fatal("bad branch accepted")
	}
	for _, limit := range []int64{1, 100} {
		data, e := g.Blob(context.Background(), c["file.txt"][1], limit)
		if limit == 1 && e == nil {
			t.Fatal("oversized blob loaded")
		}
		if limit == 100 && (e != nil || string(data) != "base\n") {
			t.Fatal(string(data), e)
		}
	}
	if yes, e := g.Active(context.Background()); e != nil || !yes {
		t.Fatal(yes, e)
	}
	if yes, e := g.IntegrationActive(context.Background(), "merge"); e != nil || !yes {
		t.Fatal(yes, e)
	}
}
func TestLiteralPathsSafePathsAndUTF8(t *testing.T) {
	g := testutil.Repo(t)
	for _, name := range []string{"/absolute/file", "../outside", ".git/config", "nested/.GIT/config"} {
		if _, e := g.SafePath(name); e == nil {
			t.Fatalf("unsafe %s", name)
		}
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(g.Dir, "linked")); e != nil {
		t.Fatal(e)
	}
	if _, e := g.SafePath("linked/file"); e == nil {
		t.Fatal("symlink parent accepted")
	}
	testutil.Commit(t, g, "[literal]*.txt", "literal", "literal")
	if got := testutil.Must(t, g, "ls-files", "--", "[literal]*.txt"); got != "[literal]*.txt" {
		t.Fatal("path interpreted as glob")
	}
	raw := append([]byte("100644 "+strings.Repeat("a", 40)+" 2\tinvalid-"), 0xff, 0)
	if _, e := git.ParseUnmerged(raw); e == nil {
		t.Fatal("non UTF8 conflict accepted")
	}
	if _, e := git.Paths([]byte{'x', 0xff, 0}); e == nil {
		t.Fatal("non UTF8 supporting file accepted")
	}
	for _, raw := range [][]byte{[]byte("missing-tab\x00"), []byte("100644 abc 4\tx\x00")} {
		if _, e := git.ParseUnmerged(raw); e == nil {
			t.Fatal("malformed index accepted")
		}
	}
}
func TestDiscoveryLinkedWorktreeAndMetadata(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Commit(t, g, "file", "base", "base")
	subdir := filepath.Join(g.Dir, "sub")
	_ = os.Mkdir(subdir, 0700)
	sub := git.Client{Dir: subdir, Env: g.Env}
	root, e := sub.Root(context.Background())
	if e != nil || root.Dir != g.Dir {
		t.Fatal(root, e)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	testutil.Must(t, g, "worktree", "add", "-b", "linked", linked, "HEAD")
	lg := git.Client{Dir: linked, Env: g.Env}
	a, _ := g.GitDir(context.Background())
	b, _ := lg.GitDir(context.Background())
	ca, _ := g.CommonDir(context.Background())
	cb, _ := lg.CommonDir(context.Background())
	if a == b || ca != cb {
		t.Fatal("checkout/common dirs confused")
	}
	marker, _ := g.Path(context.Background(), "MERGE_HEAD")
	_ = os.WriteFile(marker, []byte("evidence"), 0600)
	if _, e := g.ReadMetadata(context.Background(), "MERGE_HEAD", 2); e == nil {
		t.Fatal("unbounded metadata read")
	}
	_ = os.Remove(marker)
	_ = os.Symlink(filepath.Join(g.Dir, "file"), marker)
	if _, e := g.ReadMetadata(context.Background(), "MERGE_HEAD", 100); e == nil {
		t.Fatal("metadata symlink followed")
	}
}
func TestNativeHooksAndConfigOverrides(t *testing.T) {
	g := testutil.Repo(t)
	testutil.Commit(t, g, "base", "base", "base")
	hook := filepath.Join(g.Dir, ".git/hooks/pre-commit")
	text := "#!/bin/sh\necho hook >> hook-count\nexit 1\n"
	if e := os.WriteFile(hook, []byte(text), 0700); e != nil {
		t.Fatal(e)
	}
	testutil.Write(t, g, "base", "changed")
	testutil.Must(t, g, "add", "base")
	r, e := g.Run(context.Background(), "commit", "-m", "must fail")
	if e != nil || r.ExitCode == 0 {
		t.Fatal("hook bypassed")
	}
	b, e := os.ReadFile(filepath.Join(g.Dir, "hook-count"))
	if e != nil || string(b) != "hook\n" {
		t.Fatal("hook retried or not run")
	}
	if testutil.Must(t, g, "config", "merge.conflictStyle") != "diff3" {
		t.Fatal("conflict style not scoped")
	}
	r, e = g.Run(context.Background(), "config", "--local", "--get", "merge.conflictStyle")
	if e != nil || r.ExitCode != 1 {
		t.Fatal("override saved into config")
	}
}

func TestNativeVersionRequirement(t *testing.T) {
	for _, tc := range []struct {
		version string
		valid   bool
	}{{"2.38.5", false}, {"2.45.9", false}, {"2.46.0", true}, {"2.51.0", true}, {"2.46.0 (vendor)", true}, {"3.0.0", true}, {"unknown", false}, {"2.x.0", false}} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "git")
			script := "#!/bin/sh\nprintf '%s\\n' 'git version " + tc.version + "'\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			g := git.Client{Dir: dir, Env: testutil.SetEnv(testutil.Environment(), "PATH", dir)}
			if err := g.RequireSupported(context.Background()); (err == nil) != tc.valid {
				t.Fatal(tc.version, err)
			}
		})
	}
}
