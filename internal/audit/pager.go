// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package audit

import (
	"context"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/process"
	"io"
	"os"
	"strings"
)

func lookup(g git.Client, key string) (string, bool) {
	if g.Env == nil {
		return os.LookupEnv(key)
	}
	for _, entry := range g.Env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}
func Display(ctx context.Context, g git.Client, text string, out, diagnostics io.Writer) error {
	return DisplayReader(ctx, g, strings.NewReader(text), out, diagnostics)
}
func DisplayReader(ctx context.Context, g git.Client, input io.Reader, out, diagnostics io.Writer) error {
	direct := func() error { _, e := io.Copy(out, input); return e }
	file, ok := out.(*os.File)
	inUse, _ := lookup(g, "GIT_PAGER_IN_USE")
	if !ok || !terminal(file) || inUse != "" {
		return direct()
	}
	configured, _ := g.Text(ctx, "config", "--get", "pager.ez")
	pager, explicit := lookup(g, "GIT_PAGER")
	disabled := map[string]bool{"false": true, "no": true, "off": true, "0": true}
	enabled := map[string]bool{"true": true, "yes": true, "on": true, "1": true}
	if !explicit {
		if disabled[strings.ToLower(configured)] {
			return direct()
		}
		if configured != "" && !enabled[strings.ToLower(configured)] {
			pager = configured
		} else {
			pager, _ = g.Text(ctx, "var", "GIT_PAGER")
		}
	}
	if pager == "" || pager == "cat" {
		return direct()
	}
	env := g.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string{}, env...)
	if _, ok := lookup(g, "LESS"); !ok {
		env = append(env, "LESS=FRX")
	}
	result, e := process.Run(ctx, process.Request{Args: []string{"/bin/sh", "-c", pager}, Dir: g.Dir, Env: env, Stdin: input, Stdout: out, Stderr: diagnostics, Foreground: true})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if result.ExitCode == 126 || result.ExitCode == 127 {
		if seeker, ok := input.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return err
			}
		}
		return direct()
	}
	return e
}
