// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package terminal provides best-effort presentation, independent of operation policy.
package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nico97118/git-ez-go/internal/review"
)

type Level int

const (
	Default Level = iota
	Quiet
	Verbose
)

// Reporter serializes progress and owns at most one animation. It never returns
// presentation errors into Git or resolver policy. No colors or terminal modes
// are used; NO_COLOR is therefore respected in every mode.
type Reporter struct {
	mu     sync.Mutex
	out    io.Writer
	level  Level
	tty    bool
	secret string
	active *activity
}
type activity struct {
	label  string
	start  time.Time
	cancel context.CancelFunc
	done   chan struct{}
}

func New(out io.Writer, level Level, secret string) *Reporter {
	if out == nil {
		out = io.Discard
	}
	f, ok := out.(*os.File)
	return &Reporter{out: out, level: level, secret: secret, tty: ok && terminal(f) && os.Getenv("TERM") != "" && os.Getenv("TERM") != "dumb"}
}
func (r *Reporter) safe(s string) string {
	if r.secret != "" {
		s = strings.ReplaceAll(s, r.secret, "<redacted>")
	}
	return review.SafeText(s)
}
func (r *Reporter) clear() {
	if r.tty && r.active != nil {
		fmt.Fprint(r.out, "\r\x1b[2K")
	}
}
func (r *Reporter) line(s string) { r.clear(); fmt.Fprintln(r.out, r.safe(s)) }
func (r *Reporter) Info(format string, args ...any) {
	if r == nil || r.level == Quiet {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.line(fmt.Sprintf(format, args...))
}
func (r *Reporter) Detail(format string, args ...any) {
	if r == nil || r.level != Verbose {
		return
	}
	r.Info(format, args...)
}
func (r *Reporter) Diagnostic(format string, args ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.line(fmt.Sprintf(format, args...))
}

// Begin emits one line for pipes, and an elapsed-time spinner for capable TTYs.
// The returned stop is idempotent and joins the animation goroutine.
func (r *Reporter) Begin(ctx context.Context, label string) func() {
	if r == nil || r.level == Quiet {
		return func() {}
	}
	r.Stop()
	ctx, cancel := context.WithCancel(ctx)
	a := &activity{label: label, start: time.Now(), cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.line("git-ez: " + label + "...")
	r.active = a
	r.mu.Unlock()
	if !r.tty {
		close(a.done)
	} else {
		go func() {
			defer close(a.done)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			frames := []string{"|", "/", "-", "\\"}
			i := 0
			for {
				select {
				case <-ctx.Done():
					r.mu.Lock()
					if r.active == a {
						r.clear()
					}
					r.mu.Unlock()
					return
				case <-ticker.C:
					r.mu.Lock()
					if r.active == a {
						fmt.Fprintf(r.out, "\r\x1b[2K  %s %s (%s)", frames[i%len(frames)], r.safe(a.label), time.Since(a.start).Truncate(time.Second))
						i++
					}
					r.mu.Unlock()
				}
			}
		}()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-a.done
			r.mu.Lock()
			if r.active == a {
				r.clear()
				r.active = nil
			}
			r.mu.Unlock()
		})
	}
}
func (r *Reporter) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	a := r.active
	r.mu.Unlock()
	if a != nil {
		a.cancel()
		<-a.done
		r.mu.Lock()
		if r.active == a {
			r.clear()
			r.active = nil
		}
		r.mu.Unlock()
	}
}

// Command formats literal argv for display; no shell evaluation takes place.
func Command(args []string) string {
	out := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
		}) < 0 {
			out[i] = arg
		} else {
			out[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return strings.Join(out, " ")
}

// SetSecret updates redaction after native continuation loads model configuration.
func (r *Reporter) SetSecret(secret string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secret = secret
}

func Count(n int, singular string) string {
	word := singular
	if n != 1 {
		word += "s"
	}
	return fmt.Sprintf("%d %s", n, word)
}
