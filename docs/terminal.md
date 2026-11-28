# Terminal behavior

Git-ez keeps native Git stdout and stderr on their original streams. When Git
succeeds without model assistance, git-ez adds no success summary. An explicitly
requested `--check` still reports its execution and failures. Native
`--continue` retains Git's editor, input and exit status; assistance only resumes
at a verified subsequent rebase conflict.

## Assistance and verbosity

Integration commands (`merge`, `rebase`, `cherry-pick`, including `--continue`)
accept `--quiet`/`-q` and `--verbose`/`-v`. These control git-ez reporting only;
they do not change native Git output or get forwarded to Git. Previously these
options were rejected. The last verbosity option wins. Options following
`--check` remain literal check arguments. `audit show --verbose` retains its
existing evidence-expansion meaning; probe has no new reporting flags.

Default output reports conflict sets, configured provider/model, resolution
attempts, proposal validation and user checks. Verbose output additionally
reports actual model-request completion, locally registered tool names and
reported token counts when supplied by the framework/provider. Token counts are
observations, not estimates or progress measurements. Tool arguments, tool
results, hidden reasoning, provider response bodies and credentials are not
printed by the reporter. Framework console logging is disabled in the executable;
local audit evidence remains the diagnostic source.

Quiet suppresses assistance progress, timings and successful assisted summaries.
Errors, warnings, recovery advice, check-failure diagnostics and interactive
review remain visible. Git-ez never suppresses native Git messages.

Example (native output abbreviated, durations illustrative):

```text
$ git ez rebase main
CONFLICT (content): Merge conflict in src/network.c
git-ez: Resolving conflict set 1 (1 file)
  Model: qwen3:8b (ollama)
git-ez: Resolution attempt 1
git-ez: Resolving conflicts (waiting for model or tools)...
git-ez: Waiting for model response...
git-ez: Applying or investigating resolution...
git-ez: Validating proposal...
git-ez: Conflict set resolved (24.3s)
Successfully rebased and updated refs/heads/topic.
git-ez: Rebase completed successfully.
  1 conflict set resolved (1 file occurrence)
  Resolution time: 24.3s
  Audit: /repository/.git/ez/runs/<id>
```

Statistics cover assistance in the current invocation, including new stops adopted
by `--continue`; earlier invocations retain their own audits and counts.
A conflict set is one native Git stop handed to assistance. File occurrences sum
the number of conflicting paths at those stops, excluding supporting edits and
without counting revision attempts again. A path conflicted at two stops counts
twice. Successful summaries count only operations that completed successfully.
Resolution time is wall time spent handling those conflict sets, including model
and tool execution, proposal checks, revisions, user review and applicable
restoration. It excludes initial Git execution, subsequent Git continuation and
final validation. Waiting for a model is shown only around actual model calls;
resolver setup and scripted/custom resolvers use the broader resolution stage.

## Review and checks

Interactive review appears on stderr, including in quiet mode. It identifies the
conflict set, original conflicting paths and attempt, shows the captured proposed
diff (including supporting edits), and labels the rationale as model-reported.
The free-form rationale includes the model's reported checks and uncertainties;
it is not independent proof. Git-ez separately states its content/marker checks
and whether the user check passed, failed or was not requested. Final integration
verification remains required. Self-confidence remains in the audit and retains
its existing admission policy, but is not displayed as an objective quality score.

```text
git-ez: Review proposed resolution
Conflict set 1 | 1 file | Attempt 1
  src/network.c
Proposed diff:
...
Resolution summary and uncertainties (model-reported, not independently verified):
Preserved configuration validation. Runtime behavior has not been verified.
Validation (git-ez):
  Proposal content and conflict-marker checks passed.
  User check: passed
Proposal ready for review; final integration verification remains required.
[a] Accept / [r] Revise (reject and reprompt) / [q] Quit
Choice:
```

Existing input bindings remain accepted. A failed check cannot be accepted;
revision requires feedback. Quit/interruption retain the existing conflict
restoration and native continuation rules. No new checks or model calls are added.

Checks run with captured output. Their command and in-progress stage appear by
default, followed by a concise success message. Failure diagnostics remain visible
in quiet mode: command, exit code when available and a bounded excerpt (up to
8192 bytes per stream). Captures that could not be retained use the existing
bounded preview and warnings. A retained audit directory is identified as possibly
incomplete; no promise of complete logs is made. Existing capture and retention
policies apply.

```text
git-ez: Validation failed.
  Command: make test
  Exit code: 2
Validation stderr:
make: *** [test] Error 1
...
git-ez: Rebase completed successfully.
git-ez: Final validation failed.
  Git integration is complete. Review the errors before proceeding.
```

Git completion and validation success are separate facts. An unsuccessful final
check retains its failure exit status and never recommends continuing completed
Git integration. Other failures offer continuation only after observing an active
matching Git operation. Cleanup uncertainty suppresses automatic recovery advice;
inspect the reported retained state. Unsupported cherry-pick sequences receive
native Git advice only. Audit paths appear only for retained audits.

## Streams and terminal capabilities

Stdout carries explicitly requested results (`probe` JSON, audit reports, help,
version) and native Git stdout. Progress, assisted summaries, review, warnings,
errors and recovery instructions use stderr. Probe continues to emit a final JSON
report on stdout even on protocol failure. Audit pager behavior is unchanged.

On a capable stderr terminal, active model/tool/check stages use an ASCII spinner
with elapsed time. No percentages, colors, hidden-cursor modes or terminal-mode
changes are used. `NO_COLOR` is respected because no colors are emitted. A dumb
terminal, an unset `TERM`, redirected stderr or a pipe receives stage lines without animations,
cursor control or periodic heartbeat noise. Each animation is stopped and joined
before native Git, review prompts or final diagnostics. Native Git can emit its
own formatting and carriage returns; git-ez preserves them.

### Human output compatibility

The former stdout block (`Status:`, `Git integration completed:`, `Journal:`) has
been replaced by human summaries on stderr, omitted on native-only success. Review
also moved from stdout to stderr. Scripts parsing those human strings must adapt;
use exit codes for success/failure and offline audit evidence when needed. There
is no integration JSON-output option. The engine's structured result and audit
schema are retained. Probe JSON, audit results, help/version and existing exit-code
policies are unchanged.
