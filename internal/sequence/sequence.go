// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package sequence implements the private capture-only native sequence editor.
package sequence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nico97118/git-ez-go/internal/journal"
)

const Helper = "__git-ez-sequence"

type Capture struct {
	Directory   string
	Limit       int64
	Secrets     []string
	Phase       string
	SourceHash  string
	Hash        string
	SourceBytes int
	Redacted    bool
}

func Hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func Command(executable string) string {
	return "'" + strings.ReplaceAll(executable, "'", "'\\''") + "' " + Helper
}

// Main reads private settings from inherited descriptor 3. No secrets appear in
// the command string or environment. It never writes Git's source instructions.
func Main(args []string) int {
	if len(args) != 1 {
		return 1
	}
	f := os.NewFile(3, "capture-settings")
	if f == nil {
		return 1
	}
	defer f.Close()
	var c Capture
	if json.NewDecoder(f).Decode(&c) != nil || c.Limit <= 0 {
		return 1
	}
	report := func() { _, _ = f.Seek(0, 0); _ = f.Truncate(0); _ = json.NewEncoder(f).Encode(c); _ = f.Sync() }
	c.Phase = "failed"
	defer report()
	source, e := journal.Read(args[0], c.Limit)
	if e != nil {
		return 1
	}
	c.SourceBytes = len(source)
	c.SourceHash = Hash(source)
	for _, secret := range c.Secrets {
		if secret != "" {
			source = []byte(strings.ReplaceAll(string(source), secret, "<redacted>"))
		}
	}
	if int64(len(source)) > c.Limit {
		return 1
	}
	c.Hash = Hash(source)
	c.Redacted = c.Hash != c.SourceHash
	// Destination is a private pre-created engine directory, not Git metadata.
	root, e := os.OpenRoot(c.Directory)
	if e != nil {
		return 1
	}
	defer root.Close()
	for name, data := range map[string][]byte{"rebase-todo.txt": source, "rebase-todo.txt.sha256": []byte(c.Hash)} {
		out, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return 1
		}
		_, e = out.Write(data)
		if e == nil {
			e = out.Sync()
		}
		closeErr := out.Close()
		if e != nil || closeErr != nil {
			return 1
		}
	}
	c.Phase = "captured"
	return 0
}
func Verify(c Capture) error {
	if c.Phase != "captured" {
		return fmt.Errorf("Required rebase sequence capture unavailable")
	}
	b, e := journal.Read(filepath.Join(c.Directory, "rebase-todo.txt"), c.Limit)
	if e != nil || Hash(b) != c.Hash {
		return fmt.Errorf("Captured rebase sequence changed or unavailable")
	}
	digest, e := journal.Read(filepath.Join(c.Directory, "rebase-todo.txt.sha256"), 64)
	if e != nil || string(digest) != c.Hash {
		return fmt.Errorf("Captured rebase sequence fingerprint changed")
	}
	return nil
}
