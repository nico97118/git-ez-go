// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"github.com/nico97118/git-ez-go/internal/application"
	"github.com/nico97118/git-ez-go/internal/cli"
	"github.com/nico97118/git-ez-go/internal/sequence"
	"os"
	"os/signal"
	"syscall"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == sequence.Helper {
		os.Exit(sequence.Main(os.Args[2:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.RunCLI(ctx, os.Args[1:], version, cli.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, application.Application{})
	stop()
	os.Exit(code)
}
