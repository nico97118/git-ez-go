// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package resolver_test

import (
	"context"
	"github.com/nico97118/git-ez-go/internal/resolver"
	"github.com/nico97118/git-ez-go/internal/testutil"
	"testing"
)

func TestStrictDecision(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"rationale":"why"}`, `{"rationale":"","confidence":1}`, `{"rationale":"why","confidence":true}`, `{"rationale":"why","confidence":null}`, `{"rationale":"why","confidence":-1}`, `{"rationale":"why","confidence":1.1}`, `{"rationale":"why","confidence":1,"extra":0}`, `{"rationale":"why","confidence":1e999}`} {
		if _, e := resolver.ParseDecision([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	d, e := resolver.ParseDecision([]byte(`{"rationale":"Combined both intents","confidence":0.9}`))
	if e != nil || d.Confidence != .9 {
		t.Fatal(d, e)
	}
}
func TestDeterministicResolverNeverStagesOrContinues(t *testing.T) {
	g := testutil.Conflicted(t)
	head := testutil.Must(t, g, "rev-parse", "HEAD")
	index := testutil.Must(t, g, "ls-files", "--stage")
	text := "combined\n"
	f := &testutil.FakeResolver{Edits: map[string]*string{"file.txt": &text}, Decision: resolver.Decision{Rationale: "Combined intent", Confidence: .95}}
	d, e := f.Resolve(context.Background(), resolver.Stop{Operation: "merge", Worktree: g.Dir, Conflicts: []resolver.Conflict{{Path: "file.txt"}}})
	if e != nil || d.Validate() != nil || len(f.Calls) != 1 {
		t.Fatal(d, e)
	}
	if testutil.Must(t, g, "rev-parse", "HEAD") != head || testutil.Must(t, g, "ls-files", "--stage") != index {
		t.Fatal("resolver took lifecycle ownership")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = f.Resolve(ctx, resolver.Stop{}); e == nil || len(f.Calls) != 1 {
		t.Fatal("cancelled resolver executed")
	}
}
