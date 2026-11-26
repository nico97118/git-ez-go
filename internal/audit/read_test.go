// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package audit

import (
	"context"
	"encoding/json"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (git.Client, *journal.Journal) {
	t.Helper()
	g := testutil.Repo(t)
	common, e := g.CommonDir(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	j, e := journal.New(common, map[string]any{"created_at": "2000-01-01T00:00:00Z", "operation": "merge", "branch": "main", "conflicts": []any{}, "reviews": []any{}}, "synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { j.Close() })
	return g, j
}
func TestPythonAndLegacyGoOfflineConsultation(t *testing.T) {
	path, e := filepath.Abs("testdata/python-v4")
	if e != nil {
		t.Fatal(e)
	}
	for _, verbose := range []bool{false, true} {
		text, e := Consult(context.Background(), git.Client{}, "show", nil, &path, verbose, io.Discard)
		if e != nil || !strings.Contains(text, "0.95") || !strings.Contains(text, "+combined") {
			t.Fatal(text, e)
		}
		if strings.Contains(text, "synthetic-reference-secret") {
			t.Fatal("reference credential escaped writer")
		}
		if verbose && (!strings.Contains(text, "langchain") || !strings.Contains(text, "git diff")) {
			t.Fatal("legacy traces missing")
		}
	}
	g, j := fixture(t)
	text, e := Consult(context.Background(), g, "list", nil, nil, false, io.Discard)
	if e != nil || !strings.Contains(text, filepath.Base(j.Directory)) {
		t.Fatal(text, e)
	}
	prefix := filepath.Base(j.Directory)[:8]
	text, e = Consult(context.Background(), g, "show", &prefix, nil, false, io.Discard)
	if e != nil || !strings.Contains(text, "Unfinished record") {
		t.Fatal(text, e)
	}
	other, e := journal.New(filepath.Dir(filepath.Dir(j.Directory)), map[string]any{"operation": "merge", "branch": "main", "conflicts": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	other.Close()
	// Explicit copied-path inspection does not require basename/id agreement.
	copy := t.TempDir()
	raw, e := os.ReadFile(filepath.Join(j.Directory, "run.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(copy, "run.json"), raw, 0400); e != nil {
		t.Fatal(e)
	}
	if _, e = Consult(context.Background(), git.Client{}, "show", nil, &copy, false, io.Discard); e != nil {
		t.Fatal(e)
	}
}
func TestUnreadableMissingAndUnsafeEvidenceIsExplicit(t *testing.T) {
	g, j := fixture(t)
	j.View["reviews"] = []any{map[string]any{"attempt": 1, "status": "failed", "decision": "stop", "application": "not_staged", "artifacts": "proposal-0001"}}
	if e := j.Save(); e != nil {
		t.Fatal(e)
	}
	text, e := Consult(context.Background(), g, "show", nil, nil, false, io.Discard)
	if e != nil || !strings.Contains(text, "Capture unavailable") || !strings.Contains(text, "File capture unavailable") {
		t.Fatal(text, e)
	}
	outside := t.TempDir()
	if e = os.WriteFile(filepath.Join(outside, "private"), []byte("must-not-be-read"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(j.Directory, "proposal-0001")); e != nil {
		t.Fatal(e)
	}
	text, e = Consult(context.Background(), g, "show", nil, nil, true, io.Discard)
	if e != nil || strings.Contains(text, "must-not-be-read") || !strings.Contains(text, "Unsafe evidence link") {
		t.Fatal(text, e)
	}
	bad := filepath.Join(filepath.Dir(j.Directory), "corrupt")
	if e = os.Mkdir(bad, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(bad, "run.json"), []byte("{broken"), 0600); e != nil {
		t.Fatal(e)
	}
	var warnings strings.Builder
	if _, e = Consult(context.Background(), g, "list", nil, nil, false, &warnings); e != nil || !strings.Contains(warnings.String(), "unreadable journal") {
		t.Fatal(warnings.String(), e)
	}
	if _, e = Consult(context.Background(), g, "show", nil, &bad, false, io.Discard); e == nil {
		t.Fatal("corrupt manifest accepted")
	}
	root, e := openRoot(j.Directory)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	for _, name := range []string{"../private", "/private", "proposal-0001/private"} {
		if _, e = read(root, name, 1024); e == nil {
			t.Fatal("unsafe artifact read", name)
		}
	}
}
func TestDiscoveryPrefixAmbiguityAndRecordedDate(t *testing.T) {
	g, j := fixture(t)
	prefix := filepath.Base(j.Directory)[:8]
	other := filepath.Join(filepath.Dir(j.Directory), prefix+"older")
	if e := os.Mkdir(other, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(j.Directory, "run.json"))
	if e != nil {
		t.Fatal(e)
	}
	var d map[string]any
	if e = json.Unmarshal(raw, &d); e != nil {
		t.Fatal(e)
	}
	d["id"] = filepath.Base(other)
	d["created_at"] = "2000-01-01T00:00:00Z"
	raw, _ = json.Marshal(d)
	if e = os.WriteFile(filepath.Join(other, "run.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Consult(context.Background(), g, "show", &prefix, nil, false, io.Discard); e == nil || !strings.Contains(e.Error(), "Ambiguous") {
		t.Fatal(e)
	}
	text, e := Consult(context.Background(), g, "show", nil, nil, false, io.Discard)
	if e != nil || !strings.Contains(text, "Run: "+filepath.Base(j.Directory)) {
		t.Fatal(text, e)
	}
}

func TestRedirectedOutputNeverLaunchesPager(t *testing.T) {
	g := testutil.Repo(t)
	marker := filepath.Join(t.TempDir(), "pager-ran")
	g.Env = testutil.SetEnv(g.Env, "GIT_PAGER", "printf bad > '"+marker+"'")
	var out strings.Builder
	if e := Display(context.Background(), g, "saved report\n", &out, io.Discard); e != nil || out.String() != "saved report\n" {
		t.Fatal(out.String(), e)
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("pager ran for redirected output")
	}
}

func TestReferencedMissingOrEscapingFileIsDiagnosedWithoutReadingOutside(t *testing.T) {
	g, j := fixture(t)
	child, e := j.Child("proposal-0001")
	if e != nil {
		t.Fatal(e)
	}
	defer child.Close()
	if e = child.Write("worktree.diff", []byte("proposal diff")); e != nil {
		t.Fatal(e)
	}
	if e = child.JSON("files.json", []any{map[string]any{"path": "file.txt", "state": "file", "artifact": "../outside/private"}, map[string]any{"path": "missing.txt", "state": "file", "artifact": "absent.txt"}}); e != nil {
		t.Fatal(e)
	}
	j.View["reviews"] = []any{map[string]any{"attempt": 1, "status": "failed", "artifacts": "proposal-0001"}}
	if e = j.Save(); e != nil {
		t.Fatal(e)
	}
	text, e := Consult(context.Background(), g, "show", nil, nil, false, io.Discard)
	if e != nil || strings.Count(text, "File artifact unavailable or unsafe") != 2 {
		t.Fatal(text, e)
	}
}

// The reader streams artifacts larger than the former 64 MiB ceiling without
// building a report string or requiring model credentials.
func TestLargeArtifactConsultationStreamsWithoutAdmissionCeiling(t *testing.T) {
	g, j := fixture(t)
	capture, err := j.Capture("large.stdout")
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Repeat("x", 64<<10)
	for range 1025 {
		if _, err = io.WriteString(capture, block); err != nil {
			t.Fatal(err)
		}
	}
	if err = capture.Close(); err != nil {
		t.Fatal(err)
	}
	out := &countWriter{}
	path := j.Directory
	if err = ConsultTo(context.Background(), g, "show", nil, &path, true, io.Discard, out); err != nil {
		t.Fatal(err)
	}
	if out.bytes < capture.Bytes {
		t.Fatal("incomplete verbose output", out.bytes, capture.Bytes)
	}
}

type countWriter struct{ bytes int64 }

func (w *countWriter) Write(b []byte) (int, error) { w.bytes += int64(len(b)); return len(b), nil }

func TestMalformedAttemptAndSessionCollectionsAreNotTreatedAsAbsent(t *testing.T) {
	for _, key := range []string{"reviews", "sessions"} {
		for _, value := range []any{nil, "corrupt", map[string]any{}} {
			g, j := fixture(t)
			j.View[key] = value
			if err := j.Save(); err != nil {
				t.Fatal(err)
			}
			path := j.Directory
			if _, err := Consult(context.Background(), g, "show", nil, &path, false, io.Discard); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatal("corrupt collection treated as absent", key, err)
			}
		}
	}
}
func TestMalformedFileCollectionIsExplicit(t *testing.T) {
	for _, raw := range []string{`null`, `"corrupt"`, `{"files":"corrupt"}`} {
		g, j := fixture(t)
		child, err := j.Child("proposal-0001")
		if err != nil {
			t.Fatal(err)
		}
		if err = child.Write("files.json", []byte(raw)); err != nil {
			t.Fatal(err)
		}
		child.Close()
		j.View["reviews"] = []any{map[string]any{"attempt": 1, "status": "failed", "artifacts": "proposal-0001"}}
		if err = j.Save(); err != nil {
			t.Fatal(err)
		}
		text, err := Consult(context.Background(), g, "show", nil, nil, false, io.Discard)
		if err != nil || !strings.Contains(text, "Malformed file capture") {
			t.Fatal(text, err)
		}
	}
}
