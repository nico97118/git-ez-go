// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package resolver defines the framework-independent proposal boundary. Neither
// the interface nor its values expose staging, commits or integration control.
package resolver

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/nico97118/git-ez-go/internal/failure"
)

type Pin struct {
	OID       string
	Reference string
}
type Conflict struct {
	Path                       string
	Base, Ours, Theirs, Marked *string
	Context                    string
}
type ProposalEvidence struct {
	Directory, Feedback, UserFeedback, ValidationDiagnostic string
	Decision                                                *Decision
}
type Review struct {
	Round    int
	Feedback string
	Previous []ProposalEvidence
}

// Evidence is a framework-free reference to local resolver artifacts.
type Evidence struct {
	Runtime               string
	Directory             string
	ModelCalls, ToolCalls int
	Status                string
}

type Stop struct {
	Environment                      func() []string      `json:"-"`
	Retain                           func(Evidence) error `json:"-"`
	Operation, Worktree, Branch      string
	BeforeOID, HeadOID               string
	Target                           Pin
	Onto                             *Pin
	ResultBaseOID, ReplayBoundaryOID string
	Conflicts                        []Conflict
	Validation                       []string // literal argv, never shell text
	ArtifactDirectory                string
	Review                           *Review
}
type Decision struct {
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
}
type Resolver interface {
	Resolve(context.Context, Stop) (Decision, error)
}

func (d Decision) Validate() error {
	if strings.TrimSpace(d.Rationale) == "" {
		return failure.New(failure.Execution, "The agent must explain its resolution")
	}
	if math.IsNaN(d.Confidence) || math.IsInf(d.Confidence, 0) || d.Confidence < 0 || d.Confidence > 1 {
		return failure.New(failure.Execution, "Confidence must be a finite number between 0 and 1")
	}
	return nil
}
func ParseDecision(raw []byte) (Decision, error) {
	invalid := failure.New(failure.Execution, "Decision must contain exactly rationale and confidence")
	if !utf8.Valid(raw) {
		return Decision{}, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return Decision{}, invalid
	}
	rationale, r := fields["rationale"]
	confidence, c := fields["confidence"]
	if !r || !c || string(confidence) == "null" {
		return Decision{}, invalid
	}
	var d Decision
	if json.Unmarshal(rationale, &d.Rationale) != nil || json.Unmarshal(confidence, &d.Confidence) != nil {
		return d, invalid
	}
	return d, d.Validate()
}
