// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package audit consults saved intervention evidence without model configuration,
// provider initialization, worktree mutation or framework message deserialization.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/review"
)

const metadataLimit int64 = 16 << 20

type savedRun struct {
	directory string
	data      map[string]any
}

func openRoot(path string) (*os.Root, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot("/")
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(absolute, "/"), "/") {
		if part == "" {
			continue
		}
		info, e := root.Lstat(part)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, fmt.Errorf("Audit path must contain real directories")
		}
		next, e := root.OpenRoot(part)
		root.Close()
		if e != nil {
			return nil, e
		}
		root = next
	}
	return root, nil
}
func read(root *os.Root, name string, limit int64) ([]byte, error) {
	if name == "" || filepath.IsAbs(name) {
		return nil, fmt.Errorf("Unsafe artifact path")
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	current := root
	var opened []*os.Root
	defer func() {
		for _, r := range opened {
			r.Close()
		}
	}()
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("Unsafe artifact path")
		}
		if i == len(parts)-1 {
			file, e := current.OpenFile(part, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return nil, e
			}
			defer file.Close()
			info, e := file.Stat()
			if e != nil || !info.Mode().IsRegular() || limit < 0 || (limit > 0 && info.Size() > limit) {
				return nil, fmt.Errorf("Evidence is not bounded regular data")
			}
			if limit == 0 {
				return nil, nil
			}
			raw, e := io.ReadAll(io.LimitReader(file, limit+1))
			if int64(len(raw)) > limit {
				return nil, fmt.Errorf("Evidence exceeds read limit")
			}
			return raw, e
		}
		info, e := current.Lstat(part)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Unsafe artifact parent")
		}
		next, e := current.OpenRoot(part)
		if e != nil {
			return nil, e
		}
		opened = append(opened, next)
		current = next
	}
	return nil, fmt.Errorf("Invalid artifact")
}
func decodeRun(directory string) (savedRun, error) {
	root, e := openRoot(directory)
	if e != nil {
		return savedRun{}, e
	}
	defer root.Close()
	raw, e := read(root, "run.json", metadataLimit)
	if e != nil {
		return savedRun{}, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var data map[string]any
	if e = decoder.Decode(&data); e != nil {
		return savedRun{}, e
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return savedRun{}, fmt.Errorf("Trailing journal data")
	}
	if data["schema_version"] != json.Number("4") {
		return savedRun{}, fmt.Errorf("Unsupported journal schema")
	}
	for _, key := range []string{"id", "created_at", "operation", "branch", "status"} {
		if value, ok := data[key].(string); !ok || len(value) > 8192 {
			return savedRun{}, fmt.Errorf("Invalid journal field: %s", key)
		}
	}
	for _, key := range []string{"worktree", "target"} {
		if value, ok := data[key].(string); ok && len(value) > 8192 {
			return savedRun{}, fmt.Errorf("Invalid journal summary field: %s", key)
		}
	}
	if _, ok := data["conflicts"].([]any); !ok {
		return savedRun{}, fmt.Errorf("Invalid conflict collection")
	}
	for _, key := range []string{"reviews", "sessions"} {
		if value, present := data[key]; present {
			if _, ok := value.([]any); !ok {
				return savedRun{}, fmt.Errorf("Invalid %s collection", key)
			}
		}
	}
	if _, ok := data["integrated"].(bool); !ok {
		return savedRun{}, fmt.Errorf("Invalid integration status")
	}
	return savedRun{directory, data}, nil
}
func discover(ctx context.Context, g git.Client, warnings io.Writer) ([]savedRun, error) {
	common, e := g.CommonDir(ctx)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(common, "ez", "runs")
	if _, e = os.Lstat(path); os.IsNotExist(e) {
		return nil, nil
	}
	root, e := openRoot(path)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	dir, e := root.Open(".")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(10001)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > 10000 {
		return nil, fmt.Errorf("Audit discovery limit exceeded; use --path for a specific run")
	}
	runs := []savedRun{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		directory := filepath.Join(path, entry.Name())
		run, e := decodeRun(directory)
		if e != nil || run.data["id"] != entry.Name() {
			if warnings != nil {
				fmt.Fprintln(warnings, review.SafeText("Warning: unreadable journal: "+directory))
			}
			continue
		}
		summary := map[string]any{}
		for _, key := range []string{"id", "created_at", "operation", "branch", "status", "worktree", "target"} {
			summary[key] = run.data[key]
		}
		run.data = summary
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := text(runs[i].data["created_at"]), text(runs[j].data["created_at"])
		if a == b {
			return text(runs[i].data["id"]) > text(runs[j].data["id"])
		}
		return a > b
	})
	return runs, nil
}

// Consult is the small in-memory convenience used by component tests.
// Production CLI consultation uses ConsultTo and streams complete reports.
func Consult(ctx context.Context, g git.Client, action string, id, path *string, verbose bool, warnings io.Writer) (string, error) {
	var out strings.Builder
	e := ConsultTo(ctx, g, action, id, path, verbose, warnings, &out)
	return out.String(), e
}
func ConsultTo(ctx context.Context, g git.Client, action string, id, path *string, verbose bool, warnings, out io.Writer) error {
	if out == nil {
		return fmt.Errorf("Audit output is required")
	}
	if path != nil {
		canonical, e := filepath.EvalSymlinks(*path)
		if e != nil {
			return e
		}
		run, e := decodeRun(canonical)
		if e != nil {
			return fmt.Errorf("Cannot read resolution journal: %w", e)
		}
		return renderTo(run, verbose, out)
	}
	runs, e := discover(ctx, g, warnings)
	if e != nil {
		return e
	}
	if action == "list" {
		if len(runs) == 0 {
			_, e = io.WriteString(out, "No audit runs found.\n")
			return e
		}
		if _, e = io.WriteString(out, "ID  Started  Operation  Branch  Status  Checkout\n"); e != nil {
			return e
		}
		for _, run := range runs {
			d := run.data
			if _, e = fmt.Fprintln(out, review.SafeText(strings.Join([]string{text(d["id"]), text(d["created_at"]), text(d["operation"]), text(d["branch"]), text(d["status"]), text(d["worktree"])}, "  "))); e != nil {
				return e
			}
		}
		return nil
	}
	matches := []savedRun{}
	for _, run := range runs {
		if id == nil || strings.HasPrefix(text(run.data["id"]), *id) {
			matches = append(matches, run)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("No matching audit run found")
	}
	if id != nil && len(matches) > 1 {
		return fmt.Errorf("Ambiguous audit run identifier; use a longer prefix")
	}
	selected, e := decodeRun(matches[0].directory)
	if e != nil {
		return e
	}
	return renderTo(selected, verbose, out)
}
func text(value any) string {
	if value == nil {
		return "unavailable"
	}
	if s, ok := value.(string); ok {
		return s
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return "invalid"
	}
	return string(raw)
}

// openArtifact resolves only real directories and a regular leaf under the held
// root. Content is streamed; its size is not an admission policy.
func openArtifact(root *os.Root, name string) (*os.File, error) {
	if name == "" || filepath.IsAbs(name) {
		return nil, fmt.Errorf("Unsafe artifact path")
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	current := root
	var opened []*os.Root
	defer func() {
		for _, r := range opened {
			r.Close()
		}
	}()
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("Unsafe artifact path")
		}
		if i == len(parts)-1 {
			file, e := current.OpenFile(part, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return nil, e
			}
			info, e := file.Stat()
			if e != nil || !info.Mode().IsRegular() {
				file.Close()
				return nil, fmt.Errorf("Artifact must be regular")
			}
			return file, nil
		}
		info, e := current.Lstat(part)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Unsafe artifact parent")
		}
		next, e := current.OpenRoot(part)
		if e != nil {
			return nil, e
		}
		opened = append(opened, next)
		current = next
	}
	return nil, fmt.Errorf("Invalid artifact")
}
