// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package options represents only the supported Git option subset.
package options

import (
	"github.com/nico97118/git-ez-go/internal/failure"
	"math/big"
	"strings"
)

type Toggle uint8

const (
	Unspecified Toggle = iota
	Enabled
	Disabled
)

func (t Toggle) Present() bool { return t != Unspecified }
func (t Toggle) Bool() bool    { return t == Enabled }
func (t Toggle) Nullable() any {
	if t == Unspecified {
		return nil
	}
	return t == Enabled
}

type Signing struct {
	Policy Toggle
	Key    string
}

func (s Signing) Nullable() any {
	if s.Policy == Unspecified {
		return nil
	}
	if s.Policy == Disabled {
		return false
	}
	return s.Key
}

type Rebase struct {
	Empty                                                                       string
	KeepEmpty, ReapplyCherryPicks, ForceRebase, Autosquash, KeepBase, ForkPoint Toggle
}

func (r Rebase) Validate() error {
	if r.Empty != "" && r.Empty != "drop" && r.Empty != "keep" && r.Empty != "stop" {
		return failure.New(failure.Usage, "Rebase empty policy must be drop, keep or stop")
	}
	return nil
}
func (r Rebase) Present() bool {
	return r.Empty != "" || r.KeepEmpty.Present() || r.ReapplyCherryPicks.Present() || r.ForceRebase.Present() || r.Autosquash.Present() || r.KeepBase.Present() || r.ForkPoint.Present()
}
func (r Rebase) Flags() []string {
	var a []string
	if r.Empty != "" {
		value := r.Empty
		// Git 2.38 calls this policy ask; newer Git retains that spelling.
		if value == "stop" {
			value = "ask"
		}
		a = append(a, "--empty="+value)
	}
	a = appendToggle(a, "keep-empty", r.KeepEmpty)
	a = appendToggle(a, "reapply-cherry-picks", r.ReapplyCherryPicks)
	if r.ForceRebase == Enabled {
		a = append(a, "--force-rebase")
	} else if r.ForceRebase == Disabled {
		a = append(a, "--ff")
	}
	if r.Autosquash == Enabled {
		a = append(a, "--interactive", "--autosquash")
		if r.Empty == "" {
			a = append(a, "--empty=drop")
		}
	} else if r.Autosquash == Disabled {
		a = append(a, "--no-autosquash")
	}
	return a
}

type Commit struct {
	Message                   *string
	Signing                   Signing
	Signoff, VerifySignatures Toggle
	Log                       *big.Int
	Cleanup                   string
}

func (c Commit) Present() bool {
	return c.Message != nil || c.Signing.Policy.Present() || c.Signoff.Present() || c.VerifySignatures.Present() || c.Log != nil || c.Cleanup != ""
}
func (c Commit) Validate() error {
	if c.Log != nil && c.Log.Sign() < 0 {
		return failure.New(failure.Usage, "Merge log limit must be nonnegative")
	}
	switch c.Cleanup {
	case "", "strip", "whitespace", "verbatim", "scissors", "default":
	default:
		return failure.New(failure.Usage, "Invalid merge cleanup mode")
	}
	if c.Message != nil && strings.ContainsRune(*c.Message, 0) {
		return failure.New(failure.Usage, "Commit message must not contain NUL characters")
	}
	return nil
}
func (c Commit) Flags(verify, preparedMessage bool) []string {
	var a []string
	if verify && c.Log != nil {
		a = append(a, "--log="+c.Log.String())
	}
	if c.Cleanup != "" {
		a = append(a, "--cleanup="+c.Cleanup)
	}
	if c.Message != nil && !preparedMessage {
		a = append(a, "--message="+*c.Message)
	}
	if c.Signing.Policy == Disabled {
		a = append(a, "--no-gpg-sign")
	} else if c.Signing.Policy == Enabled {
		a = append(a, "--gpg-sign="+c.Signing.Key)
	}
	a = appendToggle(a, "signoff", c.Signoff)
	if verify {
		a = appendToggle(a, "verify-signatures", c.VerifySignatures)
	}
	return a
}
func appendToggle(args []string, name string, t Toggle) []string {
	if t == Enabled {
		return append(args, "--"+name)
	}
	if t == Disabled {
		return append(args, "--no-"+name)
	}
	return args
}
