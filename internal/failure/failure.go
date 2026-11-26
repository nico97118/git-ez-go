// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package failure separates safe user diagnostics from their underlying causes.
package failure

import (
	"context"
	"errors"
)

type Kind string

const (
	Usage         Kind = "usage"
	Configuration Kind = "configuration"
	Precondition  Kind = "precondition"
	Execution     Kind = "execution"
	Audit         Kind = "audit"
	Cleanup       Kind = "cleanup"
)

type Error struct {
	Kind    Kind
	Message string
	Cause   error
}

func (e *Error) Error() string            { return e.Message }
func (e *Error) Unwrap() error            { return e.Cause }
func New(kind Kind, message string) error { return &Error{Kind: kind, Message: message} }
func Wrap(kind Kind, message string, cause error) error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}

// ExitCode classifies domain failures. Foreground native Git status is returned
// separately by the dispatcher; it is not converted into a domain failure.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Kind {
		case Execution, Audit, Cleanup:
			return 1
		}
	}
	return 2
}

// IsKind examines wrapped and joined failures without losing cleanup uncertainty.
func IsKind(err error, kind Kind) bool {
	if err == nil {
		return false
	}
	if domain, ok := err.(*Error); ok && domain.Kind == kind {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if IsKind(cause, kind) {
				return true
			}
		}
	}
	return IsKind(errors.Unwrap(err), kind)
}
