// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package journal

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/nico97118/git-ez-go/internal/failure"
)

// Capture streams credential-filtered raw evidence to a private temporary file.
// Close atomically publishes the complete capture; disk/IO errors are explicit.
// Memory depends on the longest registered secret, not on the output size.
type Capture struct {
	mu              sync.Mutex
	journal         *Journal
	file            *os.File
	temporary, name string
	filter          *redactingWriter
	err             error
	closed          bool
	Bytes           int64
}

func (j *Journal) Capture(name string) (*Capture, error) {
	if filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsRune(name, 0) {
		return nil, failure.New(failure.Audit, "Invalid evidence name")
	}
	if e := j.Check(); e != nil {
		return nil, e
	}
	temporary := ".publish-" + ID()
	f, e := j.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, failure.Wrap(failure.Audit, "Cannot create evidence capture", e)
	}
	c := &Capture{journal: j, file: f, temporary: temporary, name: name}
	c.filter = newRedactor(f, j.secrets)
	return c, nil
}
func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, os.ErrClosed
	}
	if c.err != nil {
		return 0, c.err
	}
	n, e := c.filter.Write(p)
	if e != nil {
		c.err = failure.Wrap(failure.Audit, "Cannot retain evidence stream", e)
	}
	return n, c.err
}
func (c *Capture) Abort() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.err
	}
	c.closed = true
	return errors.Join(c.file.Close(), c.journal.root.Remove(c.temporary))
}
func (c *Capture) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.err
	}
	c.closed = true
	defer c.journal.root.Remove(c.temporary)
	if c.err == nil {
		c.err = c.filter.Flush()
	}
	if c.err == nil {
		c.err = c.file.Sync()
	}
	if info, e := c.file.Stat(); e == nil {
		c.Bytes = info.Size()
	}
	c.err = errors.Join(c.err, c.file.Close())
	if c.err == nil {
		c.err = c.journal.Check()
	}
	if c.err == nil {
		c.err = c.journal.root.Rename(c.temporary, c.name)
	}
	if c.err == nil {
		dir, e := c.journal.root.Open(".")
		if e == nil {
			e = dir.Sync()
			e = errors.Join(e, dir.Close())
		}
		c.err = e
	}
	if c.err != nil {
		c.err = failure.Wrap(failure.Audit, "Cannot publish complete evidence stream", c.err)
	}
	return c.err
}

type redactingWriter struct {
	out     io.Writer
	secrets [][]byte
	pending []byte
	overlap int
}

func newRedactor(out io.Writer, values []string) *redactingWriter {
	r := &redactingWriter{out: out}
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, values...), "<redacted>") {
		if s != "" && !seen[s] {
			seen[s] = true
			r.secrets = append(r.secrets, []byte(s))
			r.overlap = max(r.overlap, len(s)-1)
		}
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}
func (r *redactingWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		size := min(len(p), 32<<10)
		r.pending = append(r.pending, p[:size]...)
		p = p[size:]
		if e := r.drain(false); e != nil {
			return 0, e
		}
	}
	return n, nil
}
func (r *redactingWriter) Flush() error { return r.drain(true) }
func (r *redactingWriter) drain(final bool) error {
	boundary := len(r.pending) - r.overlap
	if final {
		boundary = len(r.pending)
	}
	boundary = max(0, boundary)
	position := 0
	for position < boundary {
		start, end := -1, -1
		for _, secret := range r.secrets {
			index := bytes.Index(r.pending[position:], secret)
			if index >= 0 {
				index += position
				if start < 0 || index < start {
					start, end = index, index+len(secret)
				}
			}
		}
		if start < 0 || start >= boundary {
			break
		}
		if _, e := r.out.Write(r.pending[position:start]); e != nil {
			return e
		}
		if _, e := io.WriteString(r.out, "<redacted>"); e != nil {
			return e
		}
		position = end
	}
	end := max(position, boundary)
	if _, e := r.out.Write(r.pending[position:end]); e != nil {
		return e
	}
	r.pending = append(r.pending[:0], r.pending[end:]...)
	return nil
}

// Digest verifies complete evidence in constant space, including after review.
func (j *Journal) Digest(name string) ([32]byte, int64, error) {
	var value [32]byte
	f, e := j.OpenEvidence(name)
	if e != nil {
		return value, 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	copy(value[:], h.Sum(nil))
	return value, n, e
}

// OpenEvidence opens complete regular evidence through the held private root.
func (j *Journal) OpenEvidence(name string) (*os.File, error) {
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("Unsafe evidence name")
	}
	if e := j.Check(); e != nil {
		return nil, e
	}
	f, e := j.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("Evidence must be regular")
	}
	return f, nil
}
func (j *Journal) CopyTo(destination *Journal, name string) error {
	source, e := j.OpenEvidence(name)
	if e != nil {
		return e
	}
	defer source.Close()
	capture, e := destination.Capture(name)
	if e != nil {
		return e
	}
	_, e = io.Copy(capture, source)
	return errors.Join(e, capture.Close())
}

// FilterWriter applies the journal's streaming credential filter to a diagnostic
// destination. Close flushes redaction overlap; it does not close the destination.
func (j *Journal) FilterWriter(destination io.Writer) io.WriteCloser {
	return &filteredWriter{newRedactor(destination, j.secrets)}
}

type filteredWriter struct{ *redactingWriter }

func (w *filteredWriter) Close() error { return w.Flush() }
