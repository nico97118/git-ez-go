# Working on git-ez

Communicate with the maintainer in French. Use English for code, documentation,
terminal messages and commit subjects. Project-owned sources are GPL-3.0-only.
Retain LICENSE, Python reference attribution and all third-party terms.

The functional reference is Python git-ez commit
69693f61ea5a1d668f103eb3dec0309efde08dbe. Keep any reference checkout read-only.
Normal builds and tests must be autonomous; optional reference comparisons are separate.

Keep native Git orchestration in the engine and Genkit in the agent adapter.
The resolver investigates/proposes edits; the engine owns admission, immutable
revisions, review, validation, staging, continuation and publication. Genkit owns
context estimation/compression and native tool calling. Preserve native provider
semantics, local-only observability, schema-4 LLM-focused audit and Git 2.46 minimum.
No custom context estimator, second harness, repository instructions, subagents,
new provider/config format, remote telemetry, persistent agent recovery or Windows.
No automatic fetch/push/stash/backup/worktree product features.

Use stdlib when sufficient. Dependencies require a concrete purpose and license
review. Preserve process-cleanup uncertainty, checkout lock ownership and mandatory
proposal evidence before staging. Diagnostic storage is optional and must warn on
loss without changing actual check status. Do not claim OS sandboxing.

Run gofmt, go vet ./..., go mod verify, CGO_ENABLED=0 go test ./... and native
race tests where supported. Run scripts/build.py for platform/process changes.
Use real temporary Git repositories, self-process helpers and scripted model
fixtures. Normal tests must not change global Git settings or invoke real models.
Keep local migration traceability in ignored assessment/; do not track private
evidence or require it for normal builds/tests. Public acceptance requirements
belong in docs/qualification.md. Related primitives alone do not establish coverage.
Preserve the approved lexical metadata/no-follow deviation and its regression.

Real-model testing is explicitly opt-in and separately authorized. Do not repeat
Qwen3 8B qualification merely to obtain success. Do not invoke paid services without
explicit approval. Keep native execution, compilation and semantic quality distinct.
Never erase unrelated work. Do not create remotes, push, tag or publish without
explicit maintainer authorization. Stop at the authorized milestone for review.
