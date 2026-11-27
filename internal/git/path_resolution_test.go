// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package git_test

import (
	"context"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositorySymlinkParentMatchesNativeGit(t *testing.T) {
	a := testutil.Repo(t)
	b := testutil.Repo(t)
	child := filepath.Join(b.Dir, "child")
	if e := os.Mkdir(child, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(child, filepath.Join(a.Dir, "link")); e != nil {
		t.Fatal(e)
	}
	t.Chdir(filepath.Dir(a.Dir))
	for _, path := range []string{a.Dir + "/link/..", filepath.Base(a.Dir) + "/link/.."} {
		caller := git.Client{Dir: filepath.Dir(a.Dir), Env: a.Env}
		expected := testutil.Must(t, caller, "-C", path, "rev-parse", "--show-toplevel")
		actual, e := git.New(path)
		if e != nil {
			t.Fatal(e)
		}
		actual.Env = a.Env
		got, e := actual.Text(context.Background(), "rev-parse", "--show-toplevel")
		if e != nil {
			t.Fatal(e)
		}
		if got != expected {
			t.Fatalf("selected wrong checkout: got %s, native Git selected %s", got, expected)
		}
	}
}
