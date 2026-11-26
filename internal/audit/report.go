// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/nico97118/git-ez-go/internal/review"
)

type report struct {
	out io.Writer
	err error
}

func (r *report) line(format string, values ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintln(r.out, review.SafeText(fmt.Sprintf(format, values...)))
}
func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func array(v any) []any { a, _ := v.([]any); return a }
func (r *report) artifact(root *os.Root, name string, verbose bool) {
	if name == "" || name == "unavailable" {
		r.line("Capture unavailable.")
		return
	}
	source, e := openArtifact(root, name)
	if e != nil {
		r.line("Capture unavailable or unsafe: %s", name)
		return
	}
	defer source.Close()
	info, e := source.Stat()
	if e != nil {
		r.line("Capture unavailable: %s", name)
		return
	}
	var input io.Reader = source
	if info.Size() > 1<<20 && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl")) {
		r.line("Large JSON evidence streamed without structural validation: %s", name)
	}
	if info.Size() <= 1<<20 && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl")) {
		raw, e := io.ReadAll(source)
		if e != nil {
			r.line("Capture unavailable: %s", name)
			return
		}
		input = bytes.NewReader(raw)
		if strings.HasSuffix(name, ".json") && !json.Valid(raw) {
			r.line("Malformed or incomplete JSON evidence: %s", name)
		}
		if strings.HasSuffix(name, ".jsonl") {
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.TrimSpace(line) != "" && !json.Valid([]byte(line)) {
					r.line("Malformed or incomplete event evidence: %s", name)
					break
				}
			}
		}
	}
	if !verbose {
		input = io.LimitReader(input, 8192)
	}
	if r.err == nil {
		r.err = review.WriteSafe(r.out, input)
	}
	r.line("")
	if !verbose && info.Size() > 8192 {
		r.line("[Preview truncated; complete artifact available with --verbose: %s.]", name)
	}
}

func renderTo(run savedRun, verbose bool, out io.Writer) error {
	root, e := openRoot(run.directory)
	if e != nil {
		return e
	}
	defer root.Close()
	r := &report{out: out}
	d := run.data
	r.line("# git-ez — resolution journal\n")
	r.line("Run: %s", text(d["id"]))
	r.line("Recorded status: %s", text(d["status"]))
	r.line("Operation: %s %s", text(d["operation"]), text(d["target"]))
	r.line("Branch: %s", text(d["branch"]))
	r.line("Provider/model: %s / %s", text(d["provider"]), text(d["model"]))
	r.line("Started: %s", text(d["created_at"]))
	r.line("Finished: %s", text(d["finished_at"]))
	r.line("Integration completed: %s", text(d["integrated"]))
	if d["result_oid"] != nil {
		r.line("Result revision: %s", text(d["result_oid"]))
	}
	r.line("\nThis journal explains git-ez interventions. Inspect Git for repository history.")
	r.line("Model confidence is not proof of correctness.")
	if d["status"] == "running" {
		r.line("Unfinished record; this does not establish process liveness.")
	}
	if d["error"] != nil {
		r.line("Run stopped: %s", text(d["error"]))
	}
	for _, key := range []string{"before_oid", "target_oid", "onto_oid"} {
		if d[key] != nil {
			r.line("%s: %s", key, text(d[key]))
		}
	}
	conflicts := array(d["conflicts"])
	r.line("Conflicts: %d", len(conflicts))
	for _, item := range conflicts {
		c := object(item)
		r.line("- %s (stop %s; application %s)", text(c["path"]), text(c["stop_id"]), text(c["application"]))
	}
	reviews := array(d["reviews"])
	for i, item := range reviews {
		a := object(item)
		if len(a) == 0 {
			r.line("Malformed attempt record %d.", i+1)
			continue
		}
		r.line("\n## Attempt %s — stop %s", text(a["attempt"]), text(a["stop_id"]))
		r.line("Status: %s; decision: %s; application: %s", text(a["status"]), text(a["decision"]), text(a["application"]))
		decision := object(a["resolution"])
		if rationale, ok := decision["rationale"].(string); ok && strings.TrimSpace(rationale) != "" {
			r.line("Confidence: %s", text(decision["confidence"]))
			r.line("Rationale: %s", rationale)
		} else {
			r.line("No valid finish decision recorded; confidence is unavailable.")
		}
		sessionPath, _ := a["session"].(string)
		if sessionPath != "" {
			found := false
			for _, item := range array(d["sessions"]) {
				session := object(item)
				if session["artifacts"] == sessionPath {
					found = true
					if session["status"] == "running" {
						r.line("Incomplete agent session; published counters do not establish completion or liveness.")
					}
					r.line("Agent session: %s; status %s; model requests %s; tool calls %s", sessionPath, text(session["status"]), text(session["model_calls"]), text(session["tool_calls"]))
					if session["runtime"] == "genkit" {
						if _, err := read(root, sessionPath+"/session.json", metadataLimit); err != nil {
							r.line("Agent session summary unavailable or incomplete.")
						}
					}
				}
			}
			if !found {
				r.line("Agent session reference is incomplete: %s", sessionPath)
			}
		} else {
			r.line("No direct agent-session link recorded for this attempt; inspect saved sessions with --verbose.")
		}
		if a["paths"] != nil {
			r.line("Proposed paths: %s", text(a["paths"]))
		}
		for _, key := range []string{"feedback", "user_feedback", "validation_diagnostic", "error", "restoration", "capture_error"} {
			if a[key] != nil {
				r.line("%s: %s", key, text(a[key]))
			}
		}
		if a["validation"] != nil {
			r.line("Validation: %s", text(a["validation"]))
		}
		if directory, ok := a["artifacts"].(string); ok {
			r.line("Proposal evidence: %s", directory)
			if raw, err := read(root, directory+"/diff-capture.json", metadataLimit); err == nil {
				var capture map[string]any
				if json.Unmarshal(raw, &capture) == nil && capture["truncated"] == true {
					r.line("Incomplete proposal diff capture; this is not complete approval evidence.")
				}
			}
			r.line("Proposed worktree diff:")
			r.artifact(root, directory+"/worktree.diff", verbose)
			raw, e := read(root, directory+"/files.json", metadataLimit)
			if e != nil {
				r.line("File capture unavailable.")
			} else {
				var files any
				if json.Unmarshal(raw, &files) != nil {
					r.line("Malformed file capture.")
				} else {
					fileItems, validCollection := files.([]any)
					if container, ok := files.(map[string]any); ok {
						fileItems, validCollection = container["files"].([]any)
						if len(array(container["capture_errors"])) > 0 {
							r.line("Capture errors: %s", text(container["capture_errors"]))
						}
					}
					if !validCollection {
						r.line("Malformed file capture.")
					}
					for _, item := range fileItems {
						file := object(item)
						if file["truncated"] == true || file["capture_error"] != nil {
							r.line("Incomplete file capture: %s", text(file["path"]))
						}
						if reference, ok := file["artifact"].(string); ok {
							if _, err := read(root, directory+"/"+reference, 0); err != nil {
								r.line("File artifact unavailable or unsafe: %s", reference)
							}
						} else if file["state"] == "file" {
							r.line("File artifact reference unavailable: %s", text(file["path"]))
						}
						r.line("Captured file: %s (%s)", text(file["path"]), text(file["state"]))
					}
				}
			}
		} else {
			for _, item := range array(a["conflicts"]) {
				file := object(item)
				r.line("Proposal for %s:", text(file["path"]))
				art := object(file["artifacts"])
				r.artifact(root, text(art["diff"]), verbose)
			}
		}
	}
	if len(reviews) == 0 {
		r.line("No resolution attempt was recorded.")
		for _, item := range conflicts {
			c := object(item)
			decision := object(c["resolution"])
			if len(decision) > 0 {
				r.line("%s — confidence %s: %s", text(c["path"]), text(decision["confidence"]), text(decision["rationale"]))
				r.artifact(root, text(object(c["artifacts"])["diff"]), verbose)
			}
		}
	}
	if d["validation"] != nil {
		r.line("\nFinal/run validation (not automatically attributable to every attempt): %s", text(d["validation"]))
	}
	if d["audit_errors"] != nil {
		r.line("Audit diagnostics: %s", text(d["audit_errors"]))
	}
	if verbose {
		r.line("\n## Recorded manifest")
		r.line("%s", text(d))
		r.line("\n## Saved agent and proposal artifacts")
		r.walk(root, "")
	} else {
		r.line("\nUse --verbose for saved model/tool events, configuration, instructions and artifacts.")
	}
	r.line("Files: %s", run.directory)
	return r.err
}
func (r *report) walk(root *os.Root, prefix string) {
	if r.err != nil {
		return
	}
	directory := root
	var child *os.Root
	if prefix != "" {
		parts := strings.Split(prefix, "/")
		for _, part := range parts {
			info, e := directory.Lstat(part)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				r.line("Unsafe evidence directory: %s", prefix)
				return
			}
			next, e := directory.OpenRoot(part)
			if child != nil {
				child.Close()
			}
			if e != nil {
				r.line("Evidence directory unavailable: %s", prefix)
				return
			}
			child = next
			directory = next
		}
		defer child.Close()
	}
	file, e := directory.Open(".")
	if e != nil {
		r.line("Evidence directory unavailable: %s", prefix)
		return
	}
	entries, e := file.ReadDir(-1)
	file.Close()
	if e != nil {
		r.line("Evidence directory unavailable: %s", prefix)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if r.err != nil {
			return
		}

		name := path.Join(prefix, entry.Name())
		if name == "run.json" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			r.line("Unsafe evidence link: %s", name)
			continue
		}
		if entry.IsDir() {
			if strings.Count(name, "/") > 64 {
				r.line("[Artifact depth limit reached: %s]", name)
				continue
			}
			r.walk(root, name)
			continue
		}
		r.line("\n### %s", name)
		r.artifact(root, name, true)
	}
}
