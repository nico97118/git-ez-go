// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package conflictstate

import (
	"bytes"
	"context"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRestoresOnlyTrackedFilesAndIndex(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "blob-reuse", true: "dirty-copy"}[dirty], func(t *testing.T) {
			g := testutil.Conflicted(t)
			testutil.Must(t, g, "merge", "--abort")
			binary := bytes.Repeat([]byte{0, 1, 2, 3}, 100000)
			testutil.Write(t, g, "support.bin", string(binary))
			testutil.Must(t, g, "add", "support.bin")
			testutil.Must(t, g, "commit", "-m", "support")
			// This fixture starts on main, with the other conflicting branch named feature.
			r, e := g.Run(context.Background(), "merge", "feature")
			if e != nil || r.ExitCode == 0 {
				t.Fatal(r, e)
			}
			if dirty {
				binary = []byte("dirty tracked resource")
				testutil.Write(t, g, "support.bin", string(binary))
			}
			testutil.Write(t, g, "notes", "untracked-before")
			marked, e := os.ReadFile(filepath.Join(g.Dir, "file.txt"))
			if e != nil {
				t.Fatal(e)
			}
			s, e := New(context.Background(), g)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			_, e = os.Stat(filepath.Join(s.Directory, "worktree", "support.bin"))
			if (!os.IsNotExist(e)) != dirty {
				t.Fatal("blob reuse/copy mismatch", e)
			}
			testutil.Write(t, g, "support.bin", "mutated")
			testutil.Write(t, g, "file.txt", "proposal\n")
			testutil.Write(t, g, "notes", "untracked-after")
			testutil.Write(t, g, "generated", "retain")
			if e = os.Chmod(filepath.Join(g.Dir, "file.txt"), 0755); e != nil {
				t.Fatal(e)
			}
			testutil.Must(t, g, "add", "file.txt")
			if e = s.Restore(context.Background()); e != nil {
				t.Fatal(e)
			}
			actual, _ := os.ReadFile(filepath.Join(g.Dir, "support.bin"))
			if !bytes.Equal(actual, binary) {
				t.Fatal("tracked binary not restored")
			}
			actual, _ = os.ReadFile(filepath.Join(g.Dir, "file.txt"))
			if !bytes.Equal(actual, marked) {
				t.Fatal("marked conflict not restored")
			}
			info, _ := os.Stat(filepath.Join(g.Dir, "file.txt"))
			if info.Mode().Perm() != 0644 {
				t.Fatal("mode not restored")
			}
			if testutil.Must(t, g, "ls-files", "--unmerged") == "" {
				t.Fatal("index not restored")
			}
			actual, _ = os.ReadFile(filepath.Join(g.Dir, "notes"))
			if string(actual) != "untracked-after" {
				t.Fatal("nontracked data changed")
			}
			outside := filepath.Join(t.TempDir(), "outside")
			if e = os.WriteFile(outside, []byte("private"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(filepath.Join(g.Dir, "support.bin")); e != nil {
				t.Fatal(e)
			}
			if e = os.Symlink(outside, filepath.Join(g.Dir, "support.bin")); e != nil {
				t.Fatal(e)
			}
			if e = s.Restore(context.Background()); e != nil {
				t.Fatal(e)
			}
			actual, _ = os.ReadFile(outside)
			if string(actual) != "private" {
				t.Fatal("followed destination link")
			}
		})
	}
}
func TestRestorationRetainsNontrackedBlocker(t *testing.T) {
	g := testutil.Conflicted(t)
	s, e := New(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	path := filepath.Join(g.Dir, "file.txt")
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(path, "notes"), []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Restore(context.Background()); e == nil {
		t.Fatal("nontracked blocker removed")
	}
	if _, e = os.Stat(filepath.Join(path, "notes")); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.Directory, "index")); e != nil {
		t.Fatal("backup lost", e)
	}
}

func TestSplitIndexAndOperationMetadataRestoration(t *testing.T) {
	g := testutil.Conflicted(t)
	testutil.Must(t, g, "update-index", "--split-index")
	s, e := New(context.Background(), g)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.shared == "" {
		t.Fatal("fixture missing split index")
	}
	testutil.Write(t, g, ".git/MERGE_MSG", "changed operation message")
	if e = s.VerifyControl(); e == nil {
		t.Fatal("metadata change not detected")
	}
	testutil.Write(t, g, "file.txt", "proposal\n")
	testutil.Must(t, g, "add", "file.txt")
	testutil.Must(t, g, "update-index", "--split-index")
	if e = os.Remove(s.shared); e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if e = s.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.VerifyControl(); e != nil {
		t.Fatal(e)
	}
	if testutil.Must(t, g, "ls-files", "--unmerged") == "" {
		t.Fatal("split conflict index unreadable")
	}
}
