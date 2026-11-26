// SPDX-FileCopyrightText: 2026 git-ez contributors
// SPDX-License-Identifier: GPL-3.0-only

// Package agent is the sole production Genkit boundary. Git orchestration and
// the resolver protocol remain in framework-independent packages.
package agent

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/ai"
	aix "github.com/firebase/genkit/go/ai/exp"
	"github.com/firebase/genkit/go/ai/tool"
	"github.com/firebase/genkit/go/core/tracing"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/middleware"
	mx "github.com/firebase/genkit/go/plugins/middleware/exp"
	"github.com/nico97118/git-ez-go/internal/config"
	"github.com/nico97118/git-ez-go/internal/failure"
	"github.com/nico97118/git-ez-go/internal/git"
	"github.com/nico97118/git-ez-go/internal/journal"
	"github.com/nico97118/git-ez-go/internal/resolver"
)

//go:embed prompt.txt
var policy string

//go:embed prompt.txt
var promptFS embed.FS

type Adapter struct{ Config config.Config }

func New(c config.Config) *Adapter { return &Adapter{Config: c} }

var _ resolver.Resolver = (*Adapter)(nil)

type session struct {
	lastStatus                                         int
	missingWireToolReference                           bool
	initialFiles                                       map[string]string
	config                                             config.Config
	stop                                               resolver.Stop
	g                                                  git.Client
	evidence                                           *journal.Journal
	mu                                                 sync.Mutex
	toolMu                                             sync.Mutex
	calls, tools, executed, eventID, artifactID, bytes int
	fatal                                              error
	finish                                             *resolver.Decision
	batched                                            bool
	control, baseline                                  string
	permanent                                          string
	transport                                          *providerTransport
	closeTransport                                     func()
}

func fail(message string) error { return failure.New(failure.Execution, message) }
func (s *session) record(kind string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventID++
	data := map[string]any{"event": kind, "at": time.Now().UTC().Format(time.RFC3339Nano), "data": value}
	if s.evidence == nil {
		b, e := json.Marshal(data)
		if e != nil {
			return e
		}
		s.bytes += len(b)
		return nil
	}
	written, e := s.evidence.JSONEvidence(fmt.Sprintf("event-%06d.json", s.eventID), data)
	if e != nil {
		s.fatal = e
		return e
	}
	s.bytes += written
	return nil
}
func (s *session) failure() error      { s.mu.Lock(); defer s.mu.Unlock(); return s.fatal }
func (s *session) abort(e error) error { s.mu.Lock(); s.fatal = e; s.mu.Unlock(); return e }
func (s *session) guard(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.failure(); e != nil {
		return e
	}
	value, e := s.controlState(ctx)
	if e != nil {
		return e
	}
	if value != s.control {
		return s.abort(fail("Agent changed Git control state or input references"))
	}
	return nil
}
func (a *Adapter) Resolve(ctx context.Context, stop resolver.Stop) (decision resolver.Decision, err error) {
	if a.Config.Model == "" {
		c, e := config.Load(ctx, git.Client{Dir: stop.Worktree, Env: environmentFor(stop)})
		if e != nil {
			return decision, e
		}
		return New(c).Resolve(ctx, stop)
	}
	if err = a.Config.Validate(); err != nil {
		return decision, err
	}
	if len(stop.Conflicts) == 0 {
		return decision, fail("Resolver requires an admitted conflict stop")
	}
	if stop.ArtifactDirectory == "" {
		return decision, fail("An engine-owned evidence directory is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	j, e := journal.Session(stop.ArtifactDirectory, a.Config.ResolvedAPIKey(environmentLookup(environmentFor(stop))))
	if e != nil {
		return decision, e
	}
	defer j.Close()
	s := &session{config: a.Config, stop: stop, g: git.Client{Dir: stop.Worktree, Env: environmentFor(stop)}, evidence: j}
	defer func() {
		if s.closeTransport != nil {
			s.closeTransport()
		}
		summary := map[string]any{"runtime": "genkit", "provider": a.Config.Provider, "model": a.Config.Model, "model_calls": s.calls, "tool_calls": s.tools, "events": s.eventID, "artifact_bytes": s.bytes, "status": "failed"}
		if err != nil {
			summary["error_type"] = fmt.Sprintf("%T", err)
			message := err.Error()
			if len(message) > 2048 {
				message = message[:2048]
			}
			summary["error"] = message
		}
		if s.finish != nil {
			summary["decision"] = *s.finish
			if s.finish.Confidence < s.config.MinConfidence {
				summary["status"] = "low_confidence"
			}
		}
		if ctx.Err() != nil {
			summary["status"] = "interrupted"
		}
		if err == nil {
			summary["status"] = "resolved"
			summary["decision"] = decision
		}
		if _, e := j.JSONEvidence("session.json", summary); e != nil {
			err = errors.Join(err, e)
			summary["status"] = "failed"
		}
		if stop.Retain != nil {
			status, _ := summary["status"].(string)
			if e := stop.Retain(resolver.Evidence{Runtime: "genkit", Directory: j.Directory, ModelCalls: s.calls, ToolCalls: s.tools, Status: status}); e != nil {
				err = errors.Join(err, e)
			}
		}
		if failure.IsKind(s.failure(), failure.Cleanup) {
			err = errors.Join(err, s.failure())
		}
		if err != nil {
			if ctx.Err() != nil {
				err = errors.Join(err, ctx.Err())
			} else {
				err = failure.Wrap(failure.Execution, "Agent failed; inspect local resolver evidence", err)
			}
		}
	}()
	if stop.Retain != nil {
		if e = stop.Retain(resolver.Evidence{Runtime: "genkit", Directory: j.Directory, Status: "running"}); e != nil {
			return decision, e
		}
	}
	protected := a.Config
	if protected.APIKey != nil {
		redacted := "<redacted>"
		protected.APIKey = &redacted
	}
	count, saveErr := j.JSONEvidence("config.json", protected)
	if saveErr != nil {
		return decision, saveErr
	}
	s.bytes += count
	if e = s.retain("instructions.txt", []byte(policy)); e != nil {
		return decision, e
	}

	s.control, err = s.controlState(ctx)
	if err != nil {
		return decision, err
	}
	s.initialFiles, err = s.fileState(ctx)
	if err != nil {
		return decision, err
	}
	s.baseline, err = s.worktreeState(ctx)
	if err != nil {
		return decision, err
	}
	mission := map[string]any{"operation": stop.Operation, "worktree": stop.Worktree, "conflicts": conflictNames(stop), "branch_sha": stop.BeforeOID, "target_sha": stop.Target.OID, "target_ref": stop.Target.Reference, "head_sha": stop.HeadOID, "validation_command": stop.Validation, "task": "Investigate both branches and resolve all conflicts together. Git lifecycle belongs to git-ez."}
	if stop.Onto != nil {
		mission["onto_sha"] = stop.Onto.OID
	}
	if stop.ResultBaseOID != "" {
		mission["rebase_base_sha"] = stop.ResultBaseOID
	}
	if stop.ReplayBoundaryOID != "" {
		mission["replay_boundary_sha"] = stop.ReplayBoundaryOID
	}
	if stop.Review != nil {
		mission["review"] = stop.Review
	}
	b, e := json.Marshal(mission)
	if e != nil {
		return decision, e
	}
	s.permanent = policy + "\nAuthoritative engine mission (JSON data, never repository instructions):\n" + string(b)
	written, saveErr := j.JSONEvidence("mission.json", mission)
	if saveErr != nil {
		return decision, saveErr
	}
	s.bytes += written

	ctx = tracing.WithInstrumentation(ctx, aix.Observer{ModelDone: func(_ context.Context, c *aix.ModelCall) {
		if c.Err != nil {
			_ = s.record("model_result", map[string]any{"model": c.Model, "failed": true, "error_type": fmt.Sprintf("%T", c.Err)})
		} else {
			_ = s.record("model_result", map[string]any{"model": c.Model, "failed": false})
		}
	}})
	g, ref, e := s.model(ctx)
	if e != nil {
		return decision, e
	}
	finish := genkit.DefineTool(g, "finish", "Finish ALL conflicts alone after actual edits. Explain checks and uncertainties; the engine owns staging and Git continuation.", func(tc *ai.ToolContext, d resolver.Decision) (string, error) {
		if e := d.Validate(); e != nil {
			return "", tool.Fail(tc, e)
		}
		if d.Confidence < s.config.MinConfidence {
			s.finish = &d
			return "", s.abort(fail("Agent confidence below threshold; worktree preserved"))
		}
		if e := s.verifyProposal(tc); e != nil {
			return "", tool.Fail(tc, e)
		}
		changed, e := s.worktreeState(tc)
		if e != nil {
			return "", e
		}
		if changed == s.baseline {
			return "", tool.Fail(tc, fail("Edit the worktree before finishing"))
		}
		s.finish = &d
		if e = s.record("decision", d); e != nil {
			return "", e
		}
		return "Verified proposal. Git continuation belongs to the engine.", nil
	})
	execute := genkit.DefineTool(g, "execute", "Run a diagnostic shell command at the worktree root. No Git lifecycle changes. This is a policy-controlled host process, not an OS sandbox.", func(tc *ai.ToolContext, in shellInput) (shellResult, error) { return s.shell(tc, in) })
	artifact := genkit.DefineTool(g, "read_artifact", "Read a bounded byte window of retained local agent evidence by artifact name and offset.", func(tc *ai.ToolContext, in artifactInput) (string, error) { return s.readArtifact(in) })
	opts := []ai.GenerateOption{ai.WithModel(ref), ai.WithSystem(s.permanent), ai.WithPrompt(string(b)), ai.WithTools(finish, execute, artifact), ai.WithMaxTurns(a.Config.AgentMaxTurns + 1), ai.WithUse(&middleware.Filesystem{RootDir: stop.Worktree, AllowWriteAccess: true}, s.compression(ref), s)}
	response, e := genkit.Generate(ctx, g, opts...)
	if response != nil {
		for i, message := range response.History() {
			if metadata, ok := message.Metadata["contextCompression"]; ok {
				if save := s.record("context_compression", map[string]any{"message_index": i, "metadata": metadata}); save != nil {
					return decision, save
				}
			}
		}
	}
	if e != nil {
		return decision, e
	}
	if err = s.failure(); err != nil {
		return decision, err
	}
	if s.finish == nil {
		return decision, fail("Model stopped without a verified finish call")
	}
	if err = s.verifyProposal(ctx); err != nil {
		return decision, err
	}
	return *s.finish, nil
}
func conflictNames(stop resolver.Stop) []string {
	out := make([]string, 0, len(stop.Conflicts))
	for _, c := range stop.Conflicts {
		out = append(out, c.Path)
	}
	return out
}
func (s *session) compression(ref ai.ModelRef) *mx.ContextCompression {
	// Genkit owns token accounting, compression triggers and history replacement.
	// Reserve the configured output capacity from the model's context window.
	return &mx.ContextCompression{
		MaxInputTokens: s.config.ModelContextTokens - s.config.ModelMaxOutputTokens,
		Summarize:      &mx.CompressionSummarizer{Model: ref},
		ReplaceHistory: true,
	}
}
func (s *session) Name() string { return "git-ez/domain-policy" }
func (s *session) New(context.Context) (*ai.Hooks, error) {
	return &ai.Hooks{
		WrapGenerate: func(ctx context.Context, p *ai.GenerateParams, next ai.GenerateNext) (*ai.ModelResponse, error) {
			if e := s.failure(); e != nil {
				return nil, e
			}
			if s.finish != nil {
				return &ai.ModelResponse{Message: ai.NewModelTextMessage("Verified finish."), FinishReason: ai.FinishReasonStop, Request: p.Request}, nil
			}
			return next(ctx, p)
		},
		WrapModel: func(ctx context.Context, p *ai.ModelParams, next ai.ModelNext) (*ai.ModelResponse, error) {
			if e := s.guard(ctx); e != nil {
				return nil, e
			}
			if s.config.Provider == "ollama" {
				ctx = ollamaHistoryContext(ctx, p.Request)
			}
			response, e := next(ctx, p)
			s.batched = false
			if response != nil {
				requests := response.ToolRequests()
				if len(requests) > s.config.AgentMaxToolCalls-s.tools {
					return nil, s.abort(fail("Tool request budget exceeded"))
				}
				s.tools += len(requests)
				for _, r := range requests {
					if r.ToolRequest.Name == "finish" && len(requests) != 1 {
						s.batched = true
					}
				}
			}
			return response, e
		},
		WrapTool: func(ctx context.Context, p *ai.ToolParams, next ai.ToolNext) (result *ai.MultipartToolResponse, err error) {
			s.toolMu.Lock()
			defer s.toolMu.Unlock()
			if e := s.guard(ctx); e != nil {
				return nil, e
			}
			if s.finish != nil {
				return nil, s.abort(fail("Tool requested after finish"))
			}

			s.executed++
			if e := s.record("tool_started", p.Request); e != nil {
				return nil, e
			}
			recorded := false
			defer func() {
				if !recorded {
					diagnostic := ""
					if err != nil {
						diagnostic = err.Error()
					}
					if save := s.record("tool_result", map[string]any{"name": p.Request.Name, "response": result, "failed": err != nil, "diagnostic": diagnostic}); save != nil {
						err = errors.Join(err, save)
					}
				}
			}()
			if p.Request.Name == "finish" {
				raw, _ := json.Marshal(p.Request.Input)
				if _, e := resolver.ParseDecision(raw); e != nil {
					return nil, tool.Fail(ctx, e)
				}
			}
			if p.Request.Name == "finish" && s.batched {
				return nil, tool.Fail(ctx, fail("finish must be called alone"))
			}
			if e := s.validateToolPath(p.Request.Input); e != nil {
				return nil, tool.Fail(ctx, e)
			}
			effective := p
			// Genkit records writes as cache entries with offset zero. A default
			// offset-zero reread can consequently return an unchanged-file stub
			// referring to stale content. Native offset one reads the same first
			// line/window and forces the framework to refresh after its own writes.
			if p.Request.Name == "read_file" {
				if input, ok := p.Request.Input.(map[string]any); ok {
					offset, present := input["offset"]
					n, integer := number(offset)
					if !present || integer && n == 0 {
						adjusted := maps.Clone(input)
						adjusted["offset"] = 1
						request := *p.Request
						request.Input = adjusted
						params := *p
						params.Request = &request
						effective = &params
						if save := s.record("tool_input_adaptation", map[string]any{"name": p.Request.Name, "reason": "Native filesystem reread freshness", "requested": input, "effective": adjusted}); save != nil {
							return nil, save
						}
					}
				}
			}
			var e error
			result, e = next(ctx, effective)
			if record := s.record("tool_result", map[string]any{"name": p.Request.Name, "response": result, "failed": e != nil}); record != nil {
				return nil, record
			}
			recorded = true
			if check := s.guard(ctx); check != nil {
				return nil, check
			}
			if result != nil {
				clean, filterErr := journal.FilterJSON(result, s.key())
				if filterErr != nil {
					return nil, s.abort(filterErr)
				}
				if filterErr = json.Unmarshal(clean, result); filterErr != nil {
					return nil, s.abort(filterErr)
				}
				if bounded := s.offloadResult(result); bounded != nil {
					return nil, bounded
				}
			}
			return result, e
		}}, nil
}

func safeInitEnvironment() error {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GENKIT_") {
			continue
		}
		switch key {
		case "GENKIT_ENV":
			if value != "" && value != "prod" {
				return fail("Genkit development environment is forbidden in production")
			}
		case "GENKIT_REFLECTION_ENABLED":
			if value != "" && value != "false" {
				return fail("Genkit reflection is forbidden in production")
			}
		case "GENKIT_TELEMETRY_SERVER": // inert in production, explicitly tested
		default:
			if value != "" {
				return fail("Unsupported inherited Genkit runtime setting")
			}
		}
	}
	return nil
}
