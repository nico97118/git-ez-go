// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/review"
)

// Recovery advice observes current native state; it never resumes Git or a model.
func continuationHint(ctx context.Context, g git.Client, c config.Config, q cli.Request, out io.Writer) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	active, err := g.IntegrationActive(ctx, q.Command)
	if err != nil || !active {
		return
	}
	if q.Command == "cherry-pick" {
		sequencer, err := g.HasMarker(ctx, "sequencer")
		if err != nil {
			return
		}
		if sequencer {
			fmt.Fprintln(out, "Cherry-pick sequence is still in progress. Inspect it and use native Git in the repository:\n  git cherry-pick --continue")
			return
		}
	}
	args := []string{"git", "ez", q.Command, "--continue"}
	if q.Quiet {
		args = append(args, "--quiet")
	} else if q.Verbose {
		args = append(args, "--verbose")
	}
	if q.Interactive {
		args = append(args, "--interactive")
	}
	if q.Repo != "." {
		args = append(args, "--repo", q.Repo)
	}
	if len(q.Check) > 0 {
		args = append(args, "--check")
		args = append(args, q.Check...)
	}
	lookup := func(key string) (string, bool) {
		if g.Env == nil {
			return os.LookupEnv(key)
		}
		for _, entry := range g.Env {
			k, v, ok := strings.Cut(entry, "=")
			if ok && k == key {
				return v, true
			}
		}
		return "", false
	}
	secrets := []string{c.ResolvedAPIKey(lookup)}
	// Native continuation can be interrupted before full model config is loaded.
	key, _ := g.Text(ctx, "config", "--get", "ez.apiKey")
	secrets = append(secrets, key)
	keyEnv, _ := g.Text(ctx, "config", "--get", "ez.apiKeyEnv")
	if keyEnv == "" {
		provider, _ := g.Text(ctx, "config", "--get", "ez.provider")
		keyEnv = "OPENAI_API_KEY"
		if provider == "ollama" {
			keyEnv = "LOCAL_MODEL_API_KEY"
		}
	}
	if value, ok := lookup(keyEnv); ok {
		secrets = append(secrets, value)
	}
	for i, arg := range args {
		for _, secret := range secrets {
			if secret != "" {
				arg = strings.ReplaceAll(arg, secret, "<redacted>")
			}
		}
		args[i] = shellArgument(arg)
	}
	fmt.Fprintf(out, "%s is still in progress. Resolve the remaining conflicts manually and stage them, then resume with:\n  %s\nOr use native Git in the repository:\n  git %s --continue\n", q.Command, review.SafeText(strings.Join(args, " ")), q.Command)
}
func shellArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
