// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package agent

import (
	"strings"
	"testing"
)

func TestNativeFilesystemRereadAfterMutationReturnsCurrentBytes(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		for _, method := range []string{"write_file", "edit_file"} {
			for _, offset := range []string{"omitted", "zero"} {
				t.Run(provider+"/"+method+"/"+offset, func(t *testing.T) {
					mutation := write("file.txt", "changed\n")
					expected := "changed\n</read_file>"
					if method == "edit_file" {
						mutation = step{name: "edit_file", args: map[string]any{"filePath": "file.txt", "edits": []any{map[string]any{"oldString": "<<<<<<< HEAD", "newString": "<<<<<<< HEAD\nchanged"}}}}
						expected = "HEAD\nchanged\n"
					}
					reread := read("file.txt")
					if offset == "zero" {
						reread.args["offset"] = 0
					}
					c, g, fixture, close := setup(t, provider, read("file.txt"), mutation, reread, write("file.txt", "combined\n"), finish(.95))
					defer close()
					result, err := runEngine(t, c, g)
					if err != nil || !result.Integrated {
						t.Fatal(result, err)
					}
					fixture.mu.Lock()
					defer fixture.mu.Unlock()
					found := false
					for _, raw := range fixture.requests[3]["messages"].([]any) {
						message := raw.(map[string]any)
						if message["role"] != "user" {
							continue
						}
						switch content := message["content"].(type) {
						case string:
							found = found || strings.Contains(content, expected)
						case []any:
							for _, part := range content {
								text, _ := part.(map[string]any)["text"].(string)
								found = found || strings.Contains(text, expected)
							}
						}
					}
					if !found {
						t.Fatal("The reread did not deliver current file bytes", fixture.requests[3]["messages"])
					}
				})
			}
		}
	}
}
