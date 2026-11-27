# Acceptance status

The native Go engine, Genkit protocols/context integration, interactive review,
restoration, validation, locking, process cleanup and LLM-focused schema-4 audit
have deterministic regression coverage. Native arm64 qualification exists for
Linux, macOS and FreeBSD, including installed CLI and pager/cancellation checks.
Six targets compile without CGO. Git 2.38.0 is the supported minimum; qualification
uses actual isolated Git executables, not simulated version output. See
[Git compatibility](git-compatibility.md)
for the executed version evidence and accepted native signoff limitation.

## Material acceptance requirements still open

* Successful representative semantic resolution with a sufficiently capable model,
  preserving both branches' intent and passing independent checks. Qwen3 8B did not
  establish this, and small samples do not rank Genkit against Deep Agents.
* Real-model long-context/summarization quality when genuinely needed. Native
  framework compression and physical request accounting pass scripted regressions;
  token-anchor metadata is not evidence that a real compression occurred.
* Native amd64 qualification on Linux, macOS and FreeBSD. Future CI jobs prepare
  this evidence but workflow definitions and cross-builds are not executed qualification.
* Material replay/continuation fault observations in the partial matrix: malformed
  or ambiguous replay identity, selection/topology races, and recovery under every
  relevant staging/restore fault. Existing safety tests are extensive; the entire
  historical fault-injection matrix is not claimed closed.

No confirmed material production defect is intentionally left unfixed here.
The optional-validation-output policy is now implemented: lost diagnostics warn,
retain actual status/available previews, and do not relax mandatory proposal evidence.

## Secondary or implementation-specific observations

Remaining combinations of authorship/signing/hooks, editor/empty/autosquash policies,
CLI aliases/messages, Git configuration overrides and diagnostics are individually
identified as partial; not every cross-product is a release blocker. Deep Agents
backend classes/private scratch storage, exact historical report layout and every
old internal resource limit are not automatically Go product requirements.
Historical Python writer compatibility remains optional; read compatibility is
best-effort. Manifest/discovery limits and sequence/file-policy coupling remain
explicit engineering restrictions, not silently complete resource-limit parity.

Do not claim full functional parity or unconditional production readiness. Repository
hosting, deterministic CI and packaging preparation can proceed independently of
the open real-model gate. No official release is authorized by these checks.

## OpenRouter qualification

OpenRouter is configured through the existing OpenAI-compatible provider; see the
[assessment](openrouter.md) and [opt-in real-conflict procedure](../README.md#opt-in-local-smoke-test).
Loopback scripted coverage verifies Git-loaded configuration, vendor-prefixed model
IDs, selected credentials, native routing/token options, tool-result references and
application of a proposal to a real temporary Git conflict. It does not establish
live gateway behavior or semantic resolution quality for any OpenRouter model.
Qualification requires a separately authorized probe and independently validated
real conflict for each intended model/upstream combination. Models requiring opaque
`reasoning_details` replay remain unqualified with Genkit v1.14.0. No live model was
invoked for this integration assessment.

## Audit storage admission

Before native integration or continuation mutates Git state, the engine prepares
`ez/runs` in the common Git directory and probes directory creation, file writing,
synchronization and atomic publication. The probe is removed; model-free runs
still leave no persistent run audit. An unusable audit destination stops admission.
Existing audit directories may have group/other permissions or a different owner;
actual filesystem access determines whether they are usable. No existing modes or
ownership are changed. New directories use 0700 and new artifact files use 0600
(subject to the process umask). Directory symlinks and observed directory identity
changes remain rejected. The internal safety workspace remains private and temporary.
Admission cannot guarantee later writes against permission changes or storage
failures, and shared audits carry no authenticity guarantee against other writers.

Regression coverage includes existing 0755 parents, writable shared run directories,
restrictive new artifacts, rejection of unsafe/unwritable storage, unchanged Git
HEAD/index/worktree on admission failure, and ancestor replacement during a run.
