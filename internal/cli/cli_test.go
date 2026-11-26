// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/options"
)

func TestCheckArgumentsRemainLiteral(t *testing.T) {
	for _, op := range []string{"rebase", "merge", "cherry-pick"} {
		for _, flag := range []string{"--check", "--chec", "--ch", "--c"} {
			for _, attached := range []bool{false, true} {
				name := op + flag
				if attached {
					name += "=command"
				}
				t.Run(name, func(t *testing.T) {
					args := []string{op, "main"}
					if attached {
						args = append(args, flag+"=command")
					} else {
						args = append(args, flag, "command")
					}
					tail := []string{"-S", "--gpg-sign", "--no-gpg-sign", "--ff", "--", "--check"}
					args = append(args, tail...)
					r, e := Parse(args)
					if e != nil {
						t.Fatal(e)
					}
					if !reflect.DeepEqual(r.Check, append([]string{"command"}, tail...)) || r.Commit.Signing.Policy != options.Unspecified {
						t.Fatalf("literal args changed: %+v", r)
					}
				})
			}
		}
	}
}
func TestSupportedSyntaxAndPresence(t *testing.T) {
	for _, op := range []string{"rebase", "merge", "cherry-pick"} {
		t.Run(op, func(t *testing.T) {
			r, e := Parse([]string{op, "-Skey", "main", "--signoff", "--no-signoff", "--rerere-autoupdate", "--no-rerere-autoupdate"})
			if e != nil {
				t.Fatal(e)
			}
			if *r.Target != "main" || r.Repo != "." || r.Commit.Signing.Key != "key" || r.Commit.Signoff != options.Disabled || r.RerereAutoupdate != options.Disabled {
				t.Fatalf("bad options %+v", r)
			}
			plain, e := Parse([]string{op, "main"})
			if e != nil || plain.Commit.Signoff != options.Unspecified || plain.RerereAutoupdate != options.Unspecified {
				t.Fatal("omitted policy lost")
			}
		})
	}
	for _, flag := range []string{"--force-rebase", "-f", "--no-ff"} {
		r, e := Parse([]string{"rebase", "main", flag, "--ff"})
		if e != nil || r.Rebase.ForceRebase != options.Disabled {
			t.Fatalf("last policy did not win: %+v %v", r, e)
		}
	}
	r, e := Parse([]string{"merge", "--ff-only", "main", "--no-ff", "--ff", "--log", "--cleanup=scissors", "-mfirst", "-m", "second"})
	if e != nil {
		t.Fatal(e)
	}
	r, e = r.Prepare()
	if e != nil || r.MergeFF != "ff" || r.Commit.Log.Int64() != 20 || *r.Commit.Message != "first\n\nsecond" {
		t.Fatalf("merge fields %+v %v", r, e)
	}
}
func TestUnsupportedArgumentsAndContinuationRestrictions(t *testing.T) {
	cases := [][]string{{}, {"show"}, {"probe", "--output", "file"}, {"merge", "main", "--onto", "base"}, {"merge", "main", "--autosquash"}, {"rebase", "main", "--ff-only"}, {"cherry-pick", "main", "-x"}, {"cherry-pick", "one", "two"}, {"cherry-pick", "main", "--mainline", "1"}, {"merge", "main", "--empty=keep"}, {"rebase", "main", "--empty=invalid"}, {"merge", "main", "-m", "m", "-F", "file"}, {"audit", "show", "--check", "command"}, {"probe", "--check=command"}, {"merge", "--continue", "main"}, {"rebase", "--continue", "--no-fork-point"}, {"merge", "--continue", "--no-gpg-sign"}, {"cherry-pick", "--continue", "--no-signoff"}, {"rebase", "--continue", "--no-keep-base"}, {"merge", "--continue", "--no-rerere-autoupdate"}}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, e := Parse(args); e == nil {
				t.Fatal("unsupported request accepted")
			}
		})
	}
	for _, op := range []string{"rebase", "merge", "cherry-pick"} {
		r, e := Parse([]string{op, "--continue", "--interactive", "--repo", "path", "--check", "cmd", "--"})
		if e != nil || !r.Continue || !r.Interactive || r.Target != nil {
			t.Fatalf("valid continuation %+v %v", r, e)
		}
	}
}
func TestHelpVersionAndEmptyChecks(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"rebase", "--help"}, {"audit"}, {"audit", "show", "--help"}, {"probe", "--help"}} {
		var out, err bytes.Buffer
		code := RunCLI(context.Background(), args, "test", Streams{Out: &out, Err: &err}, nil)
		if code != 0 || out.Len() == 0 || err.Len() != 0 {
			t.Fatalf("help/version %v: %d %s", args, code, err.String())
		}
	}
	for _, args := range [][]string{{"merge", "main", "--check"}, {"merge", "main", "--log=-1"}, {"merge", "main", "-F", "-"}, {"audit", "show", "run", "--path", "copy"}} {
		r, e := Parse(args)
		if e == nil {
			_, e = r.Prepare()
		}
		if e == nil {
			t.Fatalf("preflight should fail %v", args)
		}
	}
}
func TestMessagePinningAndTextPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.txt")
	if e := os.WriteFile(path, []byte("title\r\n\r\ndetails\r"), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := Parse([]string{"merge", "main", "-F", path})
	if e != nil {
		t.Fatal(e)
	}
	r, e = r.Prepare()
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte("changed"), 0600)
	if *r.Commit.Message != "title\n\ndetails\n" {
		t.Fatal("message not pinned with universal newline semantics")
	}
	for _, content := range [][]byte{[]byte("title\x00body"), {0xff}} {
		_ = os.WriteFile(path, content, 0600)
		r, e := Parse([]string{"merge", "main", "-F", path})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = r.Prepare(); e == nil {
			t.Fatal("invalid message accepted")
		}
	}
	r, _ = Parse([]string{"merge", "main", "-m", "title\x00body"})
	if _, e = r.Prepare(); e == nil {
		t.Fatal("NUL paragraph accepted")
	}
}

type recorder struct {
	called  int
	request Request
	code    int
	err     error
}

func (h *recorder) Execute(_ context.Context, r Request, _ Streams) (int, error) {
	h.called++
	h.request = r
	return h.code, h.err
}
func TestDispatchStatusAndPreparationBoundary(t *testing.T) {
	h := &recorder{code: 17}
	var out, err bytes.Buffer
	s := Streams{Out: &out, Err: &err}
	if code := RunCLI(context.Background(), []string{"merge", "--continue"}, "test", s, h); code != 17 || h.called != 1 {
		t.Fatalf("native status changed %d", code)
	}
	h.called = 0
	if code := RunCLI(context.Background(), []string{"merge", "main", "--check"}, "test", s, h); code != 2 || h.called != 0 {
		t.Fatal("invalid request dispatched")
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{failure.New(failure.Execution, "stopped"), 1}, {failure.New(failure.Precondition, "bad checkout"), 2}, {context.Canceled, 130}, {errors.New("failure"), 2}} {
		h.err = tc.err
		if code := RunCLI(context.Background(), []string{"probe"}, "test", s, h); code != tc.code {
			t.Fatalf("status %d want %d", code, tc.code)
		}
	}
	// The milestone binary fails before touching a repository; no placeholder
	// command reports success while the engine/provider/audit services are absent.
	if code := RunCLI(context.Background(), []string{"merge", "main"}, "test", s, nil); code != 2 || !strings.Contains(err.String(), "foundation build") {
		t.Fatal("missing backend misrepresented")
	}
}
