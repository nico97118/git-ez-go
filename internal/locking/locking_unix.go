//go:build linux || freebsd || darwin

// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package locking coordinates Python and Go git-ez invocations in one checkout.
package locking

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
)

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func Acquire(ctx context.Context, g git.Client) (*Lock, error) {
	directory, e := g.GitDir(ctx)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(directory, "ez-execution.lock")
	fd, e := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, failure.Wrap(failure.Precondition, "Cannot open checkout execution lock", e)
	}
	f := os.NewFile(uintptr(fd), path)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	info, e := f.Stat()
	if e != nil {
		return nil, failure.Wrap(failure.Precondition, "Cannot inspect checkout execution lock", e)
	}
	if !info.Mode().IsRegular() {
		return nil, failure.New(failure.Precondition, "Checkout execution lock must be a regular file")
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			return nil, failure.New(failure.Precondition, "Another git-ez integration is running in this checkout; wait before retrying")
		}
		return nil, failure.Wrap(failure.Precondition, "Cannot acquire checkout execution lock", e)
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	ok = true
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error { l.once.Do(func() { l.err = l.file.Close() }); return l.err }

// With keeps the same inode locked through every callback phase, including a
// caller's deferred validation/audit finalization. It never unlinks the lock.
func With(ctx context.Context, g git.Client, fn func() error) (err error) {
	l, e := Acquire(ctx, g)
	if e != nil {
		return e
	}
	defer func() {
		if e := l.Close(); err == nil {
			err = e
		}
	}()
	return fn()
}
