// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only
package options

import (
	"math/big"
	"reflect"
	"testing"
)

func TestRebaseNativeFlags(t *testing.T) {
	tests := []struct {
		options Rebase
		want    []string
	}{{Rebase{}, nil}, {Rebase{Empty: "stop"}, []string{"--empty=ask"}}, {Rebase{Autosquash: Enabled}, []string{"--interactive", "--autosquash", "--empty=drop"}}, {Rebase{Empty: "keep", KeepEmpty: Disabled, ReapplyCherryPicks: Enabled, ForceRebase: Disabled, Autosquash: Disabled}, []string{"--empty=keep", "--no-keep-empty", "--reapply-cherry-picks", "--ff", "--no-autosquash"}}}
	for _, tt := range tests {
		if got := tt.options.Flags(); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%v != %v", got, tt.want)
		}
	}
}
func TestCommitFlagsPreservePreparedMessage(t *testing.T) {
	n := big.NewInt(3)
	m := "title\n\nbody"
	c := Commit{Message: &m, Log: n, Cleanup: "scissors", Signing: Signing{Policy: Enabled, Key: "key"}, Signoff: Disabled, VerifySignatures: Enabled}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	want := []string{"--cleanup=scissors", "--gpg-sign=key", "--no-signoff"}
	if got := c.Flags(false, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared merge changed: %v", got)
	}
	full := c.Flags(true, false)
	if len(full) != 6 || full[0] != "--log=3" || full[2] != "--message="+m || full[5] != "--verify-signatures" {
		t.Fatalf("full flags %v", full)
	}
	c.Signing = Signing{Policy: Disabled}
	if got := c.Flags(false, true); got[1] != "--no-gpg-sign" {
		t.Fatal("explicit disabled signing lost")
	}
}
