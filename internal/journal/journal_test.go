// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package journal_test

import (
	"bytes"
	"encoding/json"
	"github.com/nico97118/git-ez-go/internal/journal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateAtomicPublicationAndRedaction(t *testing.T) {
	root := t.TempDir()
	secret := "token\"with\\escapes"
	j, e := journal.New(root, map[string]any{"secret": secret}, secret)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if e = j.JSON("proposal.json", map[string]any{"rationale": "prefix " + secret}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(j.Directory, "proposal.json"))
	if e != nil || strings.Contains(string(b), "token") || !strings.Contains(string(b), "redacted") {
		t.Fatal(string(b), e)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if e = os.WriteFile(outside, []byte("untouched"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(j.Directory, "collision")); e != nil {
		t.Fatal(e)
	}
	if e = j.Write("collision", []byte("new")); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(outside)
	if string(b) != "untouched" {
		t.Fatal("followed leaf symlink")
	}
	info, _ := os.Stat(filepath.Join(j.Directory, "collision"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if e = j.Write("../escape", nil); e == nil {
		t.Fatal("escaped root")
	}
}
func TestBoundedReadsRefuseUnsafeFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "source")
	if e := os.WriteFile(file, []byte("12345"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := journal.Read(file, 4); e == nil {
		t.Fatal("unbounded")
	}
	link := filepath.Join(root, "link")
	if e := os.Symlink(file, link); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{root, link} {
		if _, e := journal.Read(p, 100); e == nil {
			t.Fatal("unsafe read", p)
		}
	}
}

func TestStructuredRedactionPreservesNumbersAndBooleans(t *testing.T) {
	for _, secret := range []string{"true", "1"} {
		j, e := journal.New(t.TempDir(), map[string]any{"enabled": true, "count": 1}, secret)
		if e != nil {
			t.Fatal(e)
		}
		large := json.Number("1234567890123456789012345678901234567890")
		if e = j.JSON("typed.json", map[string]any{"enabled": true, "count": large, "value": "token " + secret}); e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(filepath.Join(j.Directory, "typed.json"))
		if e != nil {
			t.Fatal(e)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var v map[string]any
		if e = decoder.Decode(&v); e != nil || v["enabled"] != true || v["count"] != large || v["value"] != "token <redacted>" {
			t.Fatal("redaction changed JSON types or integer precision", v, e)
		}
		if e = j.Close(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestScratchPromotionSeparatesSafetyAndPersistentEvidence(t *testing.T) {
	common := t.TempDir()
	j, e := journal.Scratch(common, map[string]any{"operation": "rebase", "branch": "main", "conflicts": []any{}, "reviews": []any{}, "rebase_plan": map[string]any{"native": "private"}})
	if e != nil {
		t.Fatal(e)
	}
	workspace := j.Directory
	if _, err := os.Stat(filepath.Join(workspace, "run.json")); !os.IsNotExist(err) {
		t.Fatal("transient checkpoint masqueraded as audit", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "engine-state.json")); err != nil {
		t.Fatal(err)
	}
	if e = j.JSON("rebase-todo.txt.capture.json", map[string]any{"digest": "internal"}); e != nil {
		t.Fatal(e)
	}
	if entries, err := os.ReadDir(filepath.Join(common, "ez", "runs")); err != nil || len(entries) != 0 {
		t.Fatal("scratch created persistent run", entries, err)
	}
	if e = j.Promote(); e != nil {
		t.Fatal(e)
	}
	if !j.Persistent() || j.Directory == workspace {
		t.Fatal("not promoted")
	}
	raw, e := os.ReadFile(filepath.Join(j.Directory, "run.json"))
	if e != nil || bytes.Contains(raw, []byte("rebase_plan")) || !bytes.Contains(raw, []byte("llm_resolution")) {
		t.Fatal(string(raw), e)
	}
	if _, e = os.Stat(filepath.Join(j.Directory, "rebase-todo.txt.capture.json")); !os.IsNotExist(e) {
		t.Fatal("safety capture became audit")
	}
	if _, e = os.Stat(filepath.Join(workspace, "rebase-todo.txt.capture.json")); e != nil {
		t.Fatal("safety capture lost", e)
	}
	directory := j.Directory
	if e = j.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(workspace); !os.IsNotExist(e) {
		t.Fatal("workspace retained", e)
	}
	if _, e = os.Stat(filepath.Join(directory, "run.json")); e != nil {
		t.Fatal("audit lost", e)
	}
}
func TestScratchCleanupRefusesReplacedDirectory(t *testing.T) {
	j, e := journal.Scratch(t.TempDir(), map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	original := j.Directory + "-original"
	if e = os.Rename(j.Directory, original); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(original)
	if e = os.Mkdir(j.Directory, 0700); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(j.Directory)
	sentinel := filepath.Join(j.Directory, "user-data")
	if e = os.WriteFile(sentinel, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = j.Close(); e == nil {
		t.Fatal("replaced workspace accepted")
	}
	if _, e = os.Stat(sentinel); e != nil {
		t.Fatal("unrelated data removed", e)
	}
}

func TestExistingSharedJournalAndReplacement(t *testing.T) {
	common := t.TempDir()
	for _, path := range []string{filepath.Join(common, "ez"), filepath.Join(common, "ez", "runs")} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	j, err := journal.Scratch(common, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.Promote(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(j.Directory); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal(info, err)
	}
	if err = os.Chmod(j.Directory, 0777); err != nil {
		t.Fatal(err)
	}
	if err = j.Write("shared", []byte("evidence")); err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(common, "ez", "runs")
	if err = os.Rename(runs, runs+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(runs, 0700); err != nil {
		t.Fatal(err)
	}
	if err = j.Write("refused", nil); err == nil {
		t.Fatal("accepted replaced ancestor")
	}
}

func TestScratchRefusesUnsafeAuditStorage(t *testing.T) {
	for _, kind := range []string{"symlink", "file", "unwritable"} {
		t.Run(kind, func(t *testing.T) {
			common := t.TempDir()
			path := filepath.Join(common, "ez")
			switch kind {
			case "symlink":
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "unwritable":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				if err := os.Mkdir(path, 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(path, 0700)
			}
			if j, err := journal.Scratch(common, map[string]any{}); err == nil {
				j.Close()
				t.Fatal("unsafe storage admitted")
			}
		})
	}
}
