# Architecture

`cmd/git-ez` delegates parsing/application coordination to `internal/cli` and
`internal/application`. `internal/engine` owns native Git admission, immutable
selection, locks, review, validation, staging, continuation and publication.
`internal/resolver` is a framework-independent proposal boundary. A resolver
investigates and proposes edits; it cannot own Git lifecycle.

`internal/agent` delegates generic iteration, tool calling, filesystem tools and
context management to Genkit. Experimental compression is isolated there. The
native threshold is configured context capacity minus output reservation;
permanent policy and authoritative mission remain anchored. Nested summarization
requests count against the actual model-request policy. There is no custom token
estimator, byte/token conversion or compression algorithm.

Three compatibility corrections remain isolated in the adapter:

* Genkit's pinned native Ollama converter flattens reasoning into visible content;
  preserve separate native `thinking` and `content` fields.
* It joins batched tool results into one unnamed message; preserve independent
  named native tool-result messages.
* Filesystem writes update a cache entry with zero offset. A default reread can
  incorrectly refer to stale content. Invoke the equivalent first-line offset one
  on native reads, leaving Genkit responsible for actual IO, caching and slicing.
  Original model arguments and the effective adaptation remain separately audited.

Remove a workaround only when a published framework release corrects its cause
and the corresponding protocol/freshness regressions pass without it. Do not fork
a second generic harness or tune prompts to compensate for one weak model.

`internal/process` manages child processes; uncertain cleanup prevents restoration.
`internal/journal` separates transient engine safety checkpoints from
atomic schema-4 LLM evidence. Native engine command streams are not audit events;
Git commands initiated by the LLM are. Proposed, accepted, staged and integrated
are distinct states. Failed and interrupted attempts remain inspectable. Mandatory
proposal evidence precedes staging. Validation output files are optional diagnostics:
loss does not change the command result, but incomplete evidence is explicit and
available redacted previews remain. Metadata/proposal integrity failures stay fatal.

Audit reports stream offline through native Git pager conventions. No database,
external observability platform, repository instruction loader, persistent agent
recovery or OS sandbox is provided. The approved lexical/no-follow Git metadata
admission remains deliberate, including legitimate linked-worktree handling.

## Terminal reporting

`internal/terminal` owns serialized, best-effort stderr reporting and one joined
TTY animation at a time. The engine reports at existing conflict, attempt,
proposal and check boundaries; the agent adapter reports at native model/tool
hooks. Reporting returns no policy decisions or errors into the resolution flow.
No event bus, provider-specific renderer or new persistent state is introduced.
The application formats the existing structured result with small presentation
statistics. Review continues to render immutable captured evidence and verify its
hashes; hiding raw evidence manifests/self-confidence from the terminal does not
remove them from the journal. Native lifecycle commands mirror their original
streams while preserving the captures used by the engine. See
[terminal behavior](terminal.md) for output contracts and timing definitions.
