# Native Git compatibility

Git **2.38.0 or newer** is required. Prefer a recent maintained Git release.
The version floor qualifies git-ez's supported operations; it is not a claim
that older Git releases contain no upstream defects or security fixes.

## Signoff and cryptographic signatures

The former 2.46 minimum followed qualification of stock Git 2.38.5: continuing a
conflicted `rebase --signoff` could omit the requested `Signed-off-by` trailer.
SSH signature verification succeeded independently. The maintainer now accepts
this native limitation. Git-ez does not reject `--signoff`, edit messages, amend
commits or repair rebase metadata to compensate.

[Git 2.46.0 release notes](https://github.com/git/git/blob/v2.46.0/Documentation/RelNotes/2.46.0.txt)
identify the upstream correction. Stock 2.38.0, 2.38.5 and 2.43.0 lose the trailer
in the qualified conflict case; 2.46.0, 2.51.0 and 2.54.0 preserve it. Maintenance releases
and vendor backports have not been exhaustively bisected.

A signoff trailer is text, not a GPG or SSH cryptographic signature. Regression
tests verify real SSH signatures with explicit and configured keys, explicit
signing disable, signing failures, target signature checks and retained empty
commits. Successful GPG signing is not independently exercised by this change.

## Compatibility handling and upstream review

The existing public rebase `--empty stop` policy uses native `--empty=ask`:
Git 2.38 and 2.43 accept `ask`, whereas later Git also accepts `stop` and may warn
that `ask` is deprecated. No new version-specific execution path is needed.
Autosquash already requests interactive rebase explicitly, including on Git
before [2.44](https://github.com/git/git/blob/v2.44.0/Documentation/RelNotes/2.44.0.txt).
The existing keep-base/reapply/fork-point selection logic remains unchanged.
The cherry-pick interface does not use the newer native `--empty` option.

Review of upstream release notes from 2.39 through 2.54 also identified changes
to rebase option validation, interrupted interactive replay, hook environments,
rerere error handling, worktree protection, pseudoref terminology and index
handling. In particular, [2.41](https://github.com/git/git/blob/v2.41.0/Documentation/RelNotes/2.41.0.txt)
fixed native split-index/fsmonitor corruption cases, and
[2.45](https://github.com/git/git/blob/v2.45.0/Documentation/RelNotes/2.45.0.txt)
fixed rerere crashes on corrupt/incompletely resolved input. These are
additional reasons to prefer maintained Git; the executed suite does not certify
every native configuration or corrupt repository. No git-ez workaround is added.

Git-ez reads structured Git output, OIDs, index stages and native operation
metadata rather than matching localized conflict advice. It retains native
configuration, hooks and signing. Linked worktrees, split-index capture/restore,
rerere training, replay selection, empty policies, hooks, failure preservation
and metadata checks stay in the standard deterministic suite. Newer repository
formats/extensions still require a Git executable that understands them.

## Executed qualification (2026-11-27)

Native execution used macOS arm64 and Go 1.27.4. Git 2.38.0, 2.38.5, 2.43.0,
2.46.0 and 2.54.0 were official source builds in isolated prefixes; host Git
2.51.0 was the existing Homebrew executable. Executable paths, versions and
archive/executable hashes are retained in ignored local assessment evidence.

| Git | Executed compatibility scope | Conflicted rebase signoff |
|---|---|---|
| 2.38.0 | Complete deterministic Go suite; installed interactive CLI, audit, signals, paths, binary/manual installation | Native and assisted both omit trailer; SSH verification passes |
| 2.38.5 | Git/options/installer suites; engine compatibility scenarios and focused signing/hook/worktree completion | Native and assisted both omit trailer; SSH verification passes |
| 2.43.0 | Same intermediate scope | Native and assisted both omit trailer; SSH verification passes |
| 2.46.0 | Same intermediate scope | Native and assisted preserve trailer; SSH verification passes |
| 2.51.0 (host) | Complete deterministic Go suite; all native race packages pass across the initial run and complete engine retry | Native and assisted preserve trailer; SSH verification passes |
| 2.54.0 | Complete engine/Git/options/installer suites | Native and assisted preserve trailer; SSH verification passes |

Initial over-concurrent complete suite attempts reached overall package deadlines
on the minimum and host Git; broad intermediate engine attempts also expired.
These attempts are not passes. The minimum and host normal suites were rerun completely with less load
and a 30-minute local deadline. Intermediate results claim the relevant executed
scenarios, not a complete engine package pass. No old-Git test was skipped to
hide a behavioral failure. The initial host race invocation passed all packages
except engine, which reached its 20-minute total deadline; a complete engine
race retry passed. No race was reported. Native installer qualification remains an explicit
separate invocation, as in the existing CI design.

## Required regression qualification

Use actual executables on PATH, with recorded versions and source provenance.
Version-parser/installer fixtures test admission boundaries only; they do not
establish native compatibility. Do not replace system Git or global configuration.

* Run the complete deterministic Go suite on stock Git 2.38.0 and current Git.
* Run relevant engine compatibility/differential scenarios on stock 2.38.5,
  2.43.0 and 2.46.0, plus the Git, options and installer suites.
* Compare equivalent native and assisted conflicted rebases: continuation status,
  message/trailers, author, tree, direct parent, branch, replay count, clean index
  and worktree, removed operation metadata, and cryptographic verification.
  On 2.46+ additionally require the expected trailer. Do not skip older Git.
* Keep retained-empty signoff/signature assertions and all integrity, recovery,
  staging and hook checks. Qualify installed interactive CLI, audit, signals,
  repository paths, installer and manual with minimum Git.

CI has one Linux amd64 `git-minimum` job. It builds the official 2.38.0 source
archive under an isolated prefix, verifies its SHA-256 and selected executable,
and runs the full standard Go and race suites plus installed CLI checks. Native
platform jobs keep their normal Git. Release qualification reuses the entire CI
workflow, so publication depends on the minimum-version job as well. Workflow
validation alone is not an executed Linux qualification.

The local qualification on macOS arm64 is recorded in the repository's ignored
assessment evidence. Cross-compilation is separate from native execution; the
open platform and real-model acceptance gates in [qualification](qualification.md)
remain applicable. No real LLM service is needed for this qualification.
