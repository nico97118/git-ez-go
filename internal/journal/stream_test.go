// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package journal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingRedactionAcrossEveryBoundary(t *testing.T) {
	input := "prefix token-with-secret overlap-long <redacted> suffix token-with-secret"
	expected := "prefix <redacted> <redacted> <redacted> suffix <redacted>"
	for chunk := 1; chunk <= len(input); chunk++ {
		var out bytes.Buffer
		filter := newRedactor(&out, []string{"token-with-secret", "overlap", "overlap-long", "redacted"})
		for start := 0; start < len(input); start += chunk {
			if _, err := filter.Write([]byte(input[start:min(start+chunk, len(input))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := filter.Flush(); err != nil {
			t.Fatal(err)
		}
		if out.String() != expected {
			t.Fatalf("chunk %d: %q", chunk, out.String())
		}
	}
}

func TestStreamingCapturePublishesCompletePrivateEvidence(t *testing.T) {
	j, err := New(t.TempDir(), map[string]any{}, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	capture, err := j.Capture("large.stdout")
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Repeat("0123456789", 4096) + "secret-value\n"
	for range 40 {
		if _, err = capture.Write([]byte(block)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(j.Directory, "large.stdout")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published partial evidence", err)
	}
	if err = capture.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(j.Directory, "large.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Repeat(strings.ReplaceAll(block, "secret-value", "<redacted>"), 40)
	if string(raw) != expected || capture.Bytes != int64(len(expected)) {
		t.Fatal("incomplete capture", len(raw), capture.Bytes)
	}
	info, _ := os.Stat(filepath.Join(j.Directory, "large.stdout"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	digest, size, err := j.Digest("large.stdout")
	if err != nil || size != capture.Bytes || digest == [32]byte{} {
		t.Fatal(size, err)
	}
	child, err := j.Child("copy")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err = j.CopyTo(child, "large.stdout"); err != nil {
		t.Fatal(err)
	}
	second, n, err := child.Digest("large.stdout")
	if err != nil || second != digest || n != size {
		t.Fatal(n, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestStreamingRedactionPropagatesStorageFailure(t *testing.T) {
	filter := newRedactor(failingWriter{}, nil)
	_, err := filter.Write([]byte(strings.Repeat("x", 4096)))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	j, err := New(t.TempDir(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	c, err := j.Capture("failed")
	if err != nil {
		t.Fatal(err)
	}
	c.file.Close()
	if _, err = c.Write([]byte(strings.Repeat("x", 4096))); err == nil {
		t.Fatal("ignored write error")
	}
	if err = c.Close(); err == nil {
		t.Fatal("published failed capture")
	}
	if _, err = os.Stat(filepath.Join(j.Directory, "failed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published failed capture", err)
	}
}
