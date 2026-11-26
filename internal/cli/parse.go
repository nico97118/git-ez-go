// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package cli implements the supported command grammar. Parsing never loads a
// model, reads Git configuration, or changes a repository.
package cli

import (
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/number"
	"github.com/nico97118/git-ez-go/internal/options"
)

type Action uint8

const (
	Run Action = iota
	Help
	Version
)

type Request struct {
	Action                Action
	Command, AuditCommand string
	Target                *string
	Continue, Interactive bool
	Repo                  string
	Check                 []string
	Onto                  *string
	Rebase                options.Rebase
	Commit                options.Commit
	RerereAutoupdate      options.Toggle
	MergeFF               string
	MessageParagraphs     []string
	MessageFile           *string
	RunID, AuditPath      *string
	Verbose               bool
}

func usage(message string) error { return failure.New(failure.Usage, message) }

type flagSpec struct {
	name, short string
	arity       int
}

// arity 0 is a switch, 1 requires a value, 2 is optional signing input.
func specs(command, subcommand string) []flagSpec {
	s := []flagSpec{{"help", "h", 0}}
	if command == "" {
		return append(s, flagSpec{"version", "", 0})
	}
	if command == "audit" {
		if subcommand == "show" {
			return append(s, flagSpec{"path", "", 1}, flagSpec{"verbose", "", 0})
		}
		return s
	}
	if command == "probe" {
		return s
	}
	s = append(s, flagSpec{"interactive", "", 0}, flagSpec{"continue", "", 0}, flagSpec{"repo", "", 1}, flagSpec{"check", "", 1}, flagSpec{"gpg-sign", "S", 2}, flagSpec{"no-gpg-sign", "", 0}, flagSpec{"signoff", "", 0}, flagSpec{"no-signoff", "", 0}, flagSpec{"rerere-autoupdate", "", 0}, flagSpec{"no-rerere-autoupdate", "", 0})
	if command == "rebase" {
		s = append(s, flagSpec{"onto", "", 1}, flagSpec{"empty", "", 1}, flagSpec{"force-rebase", "f", 0}, flagSpec{"no-ff", "", 0}, flagSpec{"ff", "", 0})
		for _, name := range []string{"keep-base", "fork-point", "autosquash", "keep-empty", "reapply-cherry-picks"} {
			s = append(s, flagSpec{name, "", 0}, flagSpec{"no-" + name, "", 0})
		}
	} else if command == "merge" {
		s = append(s, flagSpec{"ff", "", 0}, flagSpec{"no-ff", "", 0}, flagSpec{"ff-only", "", 0}, flagSpec{"message", "m", 1}, flagSpec{"file", "F", 1}, flagSpec{"log", "", 1}, flagSpec{"no-log", "", 0}, flagSpec{"cleanup", "", 1}, flagSpec{"verify-signatures", "", 0}, flagSpec{"no-verify-signatures", "", 0})
	}
	return s
}
func isOption(s string) bool {
	return len(s) > 1 && s[0] == '-' && !regexp.MustCompile(`^-(?:[0-9]+|[0-9]*\.[0-9]+)$`).MatchString(number.DecimalASCII(s))
}
func longFlag(name string, spec []flagSpec) (flagSpec, error) {
	for _, s := range spec {
		if name == s.name {
			return s, nil
		}
	}
	matches := []flagSpec{}
	for _, s := range spec {
		if strings.HasPrefix(s.name, name) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return flagSpec{}, usage("Ambiguous option --" + name)
	}
	return flagSpec{}, nil
}
func Parse(input []string) (Request, error) {
	r := Request{Repo: "."}
	args := append([]string(nil), input...)
	var check []string
	checkSet := false
	// This deliberately preserves the reference pre-pass, including abbreviated
	// --check and literal arguments after it, before any other flag interpretation.
	for i, arg := range args {
		if arg == "--" {
			break
		}
		opt, value, attached := strings.Cut(arg, "=")
		if strings.HasPrefix(opt, "--") && strings.HasPrefix("--check", opt) {
			checkSet = true
			check = []string{}
			if attached {
				check = append(check, value)
			}
			check = append(check, args[i+1:]...)
			args = args[:i]
			break
		}
		if arg == "--log" {
			args[i] = "--log=20"
		}
		if arg == "--gpg-sign" || arg == "-S" {
			args[i] = "--gpg-sign="
		}
	}
	var unknown []string
	literal := false
	apply := func(name, value string) error {
		enable := options.Enabled
		if strings.HasPrefix(name, "no-") {
			enable = options.Disabled
		}
		switch name {
		case "help":
			r.Action = Help
		case "version":
			r.Action = Version
		case "interactive":
			r.Interactive = true
		case "continue":
			r.Continue = true
		case "repo":
			r.Repo = pathArgument(value)
		case "onto":
			r.Onto = &value
		case "empty":
			if value != "drop" && value != "keep" && value != "stop" {
				return usage("--empty must be drop, keep or stop")
			}
			r.Rebase.Empty = value
		case "gpg-sign":
			r.Commit.Signing = options.Signing{Policy: options.Enabled, Key: value}
		case "no-gpg-sign":
			r.Commit.Signing = options.Signing{Policy: options.Disabled}
		case "signoff", "no-signoff":
			r.Commit.Signoff = enable
		case "rerere-autoupdate", "no-rerere-autoupdate":
			r.RerereAutoupdate = enable
		case "keep-base", "no-keep-base":
			r.Rebase.KeepBase = enable
		case "fork-point", "no-fork-point":
			r.Rebase.ForkPoint = enable
		case "autosquash", "no-autosquash":
			r.Rebase.Autosquash = enable
		case "keep-empty", "no-keep-empty":
			r.Rebase.KeepEmpty = enable
		case "reapply-cherry-picks", "no-reapply-cherry-picks":
			r.Rebase.ReapplyCherryPicks = enable
		case "force-rebase":
			r.Rebase.ForceRebase = options.Enabled
		case "ff", "no-ff", "ff-only":
			if r.Command == "rebase" {
				r.Rebase.ForceRebase = options.Disabled
				if name == "no-ff" {
					r.Rebase.ForceRebase = options.Enabled
				}
			} else {
				r.MergeFF = name
			}
		case "message":
			if r.MessageFile != nil {
				return usage("--message and --file are mutually exclusive")
			}
			r.MessageParagraphs = append(r.MessageParagraphs, value)
		case "file":
			if len(r.MessageParagraphs) > 0 {
				return usage("--message and --file are mutually exclusive")
			}
			value = pathArgument(value)
			r.MessageFile = &value
		case "log":
			n, e := number.Integer(value)
			if e != nil {
				return usage("--log requires an integer")
			}
			r.Commit.Log = n
		case "no-log":
			r.Commit.Log = big.NewInt(0)
		case "cleanup":
			switch value {
			case "strip", "whitespace", "verbatim", "scissors", "default":
			default:
				return usage("Invalid merge cleanup mode")
			}
			r.Commit.Cleanup = value
		case "verify-signatures", "no-verify-signatures":
			r.Commit.VerifySignatures = enable
		case "path":
			value = pathArgument(value)
			r.AuditPath = &value
		case "verbose":
			r.Verbose = true
		default:
			return usage("Unsupported option")
		}
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !literal && arg == "--" {
			literal = true
			continue
		}
		available := specs(r.Command, r.AuditCommand)
		if !literal && isOption(arg) {
			if strings.HasPrefix(arg, "--") {
				name, value, attached := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
				flag, e := longFlag(name, available)
				if e != nil {
					return r, e
				}
				if flag.name == "" {
					unknown = append(unknown, arg)
					continue
				}
				if flag.arity == 0 && attached {
					return r, usage("Option --" + name + " takes no value")
				}
				if !attached && flag.arity > 0 {
					if i+1 < len(args) && !isOption(args[i+1]) && args[i+1] != "--" {
						i++
						value = args[i]
					} else if flag.arity == 1 {
						return r, usage("Option --" + name + " requires a value")
					}
				}
				if e = apply(flag.name, value); e != nil {
					return r, e
				}
			} else {
				type shortAction struct{ name, value string }
				var actions []shortAction
				cluster := arg[1:]
				bad := false
				for j := 0; j < len(cluster); j++ {
					var flag flagSpec
					for _, s := range available {
						if s.short == string(cluster[j]) {
							flag = s
							break
						}
					}
					if flag.name == "" {
						if j == 0 {
							unknown = append(unknown, arg)
							bad = true
							break
						}
						return r, usage("Unsupported short option in " + arg)
					}
					value := ""
					if flag.arity > 0 {
						value = cluster[j+1:]
						if value == "" && i+1 < len(args) && !isOption(args[i+1]) && args[i+1] != "--" {
							i++
							value = args[i]
						} else if value == "" && flag.arity == 1 {
							return r, usage("Option -" + flag.short + " requires a value")
						}
						j = len(cluster)
					}
					actions = append(actions, shortAction{flag.name, value})
				}
				if bad {
					continue
				}
				for _, a := range actions {
					if e := apply(a.name, a.value); e != nil {
						return r, e
					}
					if r.Action != Run {
						return r, nil
					}
				}
			}
			if r.Action != Run {
				return r, nil
			}
			continue
		}
		if r.Command == "" {
			switch arg {
			case "rebase", "merge", "cherry-pick", "audit", "probe":
				r.Command = arg
				literal = false
			default:
				return r, usage("Unsupported command: " + arg)
			}
			continue
		}
		if r.Command == "audit" && r.AuditCommand == "" {
			if arg != "list" && arg != "show" {
				return r, usage("Unsupported audit command: " + arg)
			}
			r.AuditCommand = arg
			literal = false
			continue
		}
		if r.Command == "probe" || r.Command == "audit" && r.AuditCommand == "list" {
			unknown = append(unknown, arg)
			continue
		}
		if r.Command == "audit" {
			if r.RunID == nil {
				value := arg
				r.RunID = &value
			} else {
				unknown = append(unknown, arg)
			}
		} else if r.Target == nil {
			value := arg
			r.Target = &value
		} else {
			unknown = append(unknown, arg)
		}
	}
	if r.Command == "" {
		return r, usage("A command is required")
	}
	if len(unknown) > 0 {
		return r, usage("Unsupported arguments: " + strings.Join(unknown, " "))
	}
	if checkSet {
		if r.Command != "rebase" && r.Command != "merge" && r.Command != "cherry-pick" {
			return r, usage("--check is only supported for rebase, merge and cherry-pick")
		}
		r.Check = check
	}
	if r.Command == "audit" {
		if r.AuditCommand == "" {
			r.Action = Help
		}
		return r, nil
	}
	if r.Command == "probe" {
		return r, nil
	}
	if r.Continue {
		if r.Target != nil {
			return r, usage("--continue cannot be combined with a target")
		}
		if r.Onto != nil || r.Rebase.Present() || r.Commit.Present() || r.MergeFF != "" || r.RerereAutoupdate.Present() || r.MessageFile != nil || len(r.MessageParagraphs) > 0 {
			return r, usage("--continue only accepts --repo, --interactive and --check")
		}
	} else if r.Target == nil {
		return r, usage("A target or --continue is required")
	}
	return r, nil
}

// Prepare pins message-file content before any configuration/engine admission.
// It deliberately performs no Git work and leaves engine-specific combinations
// (such as --onto with --keep-base) for engine admission in Phase 3.
func (r Request) Prepare() (Request, error) {
	if r.Action != Run {
		return r, nil
	}
	if r.Check != nil && len(r.Check) == 0 {
		return r, usage("--check requires an executable")
	}
	if r.AuditPath != nil && r.RunID != nil {
		return r, usage("--path and a run identifier are mutually exclusive")
	}
	if r.MessageFile != nil {
		if *r.MessageFile == "-" {
			return r, usage("--file requires a message file; standard input is unsupported")
		}
		b, e := os.ReadFile(*r.MessageFile)
		if e != nil {
			return r, failure.Wrap(failure.Precondition, "Cannot read merge message file", e)
		}
		if !utf8.Valid(b) {
			return r, usage("Merge message file must be UTF-8")
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
		r.Commit.Message = &text
	} else if len(r.MessageParagraphs) > 0 {
		message := strings.Join(r.MessageParagraphs, "\n\n")
		r.Commit.Message = &message
	}
	if e := r.Commit.Validate(); e != nil {
		return r, e
	}
	if e := r.Rebase.Validate(); e != nil {
		return r, e
	}
	return r, nil
}
func pathArgument(value string) string {
	prefix := ""
	if strings.HasPrefix(value, "/") {
		prefix = "/"
	}
	if strings.HasPrefix(value, "//") && !strings.HasPrefix(value, "///") {
		prefix = "//"
	}
	var parts []string
	for _, p := range strings.Split(value, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	v := prefix + strings.Join(parts, "/")
	if v == "" {
		return "."
	}
	return v
}
func (r Request) String() string { return fmt.Sprintf("git ez %s", r.Command) }
