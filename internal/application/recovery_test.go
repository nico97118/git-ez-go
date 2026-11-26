// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package application

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/testutil"
)

func TestNativeRecoveryHintRedactsCredentialsBeforeShellQuoting(t *testing.T) {
	g := testutil.Conflicted(t)
	direct := "native'credential"
	environment := "environment'credential"
	configured := "selected'credential"
	testutil.Must(t, g, "config", "ez.apiKey", direct)
	testutil.Must(t, g, "config", "ez.apiKeyEnv", "QUALIFICATION_TOKEN")
	g.Env = testutil.SetEnv(g.Env, "QUALIFICATION_TOKEN", environment)
	for _, loaded := range []bool{false, true} {
		c := config.Defaults()
		if loaded {
			c.APIKey = &configured
		}
		q := cli.Request{Command: "merge", Continue: true, Interactive: true, Repo: "path's space", Check: []string{"validate", "--token", direct, "--env", environment, "--selected", configured}}
		var out bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		continuationHint(ctx, g, c, q, &out)
		text := out.String()
		if !strings.Contains(text, "git ez merge --continue --interactive --repo 'path'\"'\"'s space'") || strings.Contains(text, direct) || strings.Contains(text, environment) || strings.Contains(text, "native'\"'\"'credential") || strings.Contains(text, "environment") {
			t.Fatal(text)
		}
		if loaded && strings.Contains(text, configured) {
			t.Fatal("selected secret escaped", text)
		}
		if strings.Count(text, "<redacted>") < 2 {
			t.Fatal(text)
		}
	}
}
func TestRecoveryArgumentQuotingDoesNotExecuteShellSyntax(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	for _, value := range []string{"", "simple", "O'Reilly", "$(touch " + marker + ")", "line\nsecond", "semi;touch " + marker} {
		raw, err := exec.Command("/bin/sh", "-c", "printf '%s' "+shellArgument(value)).Output()
		if err != nil || string(raw) != value {
			t.Fatal(string(raw), err, value)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell injection executed", err)
	}
}
