# Phase 8: repository readiness and release engineering

## Outcome and boundaries

The migration workspace is prepared as an autonomous Go repository with `main` as
its initial branch. The initial commit uses the maintainer's existing Git identity;
no agent identity override or fabricated phase history is used. No remote, tag,
push or release is created. Public repository/module naming remains a maintainer
decision; the existing module path is retained pending that decision.

The repository is ready for maintainer review and hosting preparation, not an
unconditional production release. [Acceptance status](readiness.md) distinguishes
open real-model quality, native amd64 and fault-matrix evidence from confirmed defects.
No additional Qwen3 8B or paid inference is performed in this phase.

## Repository and archive inventory

`cmd/` contains the CLI, `internal/` cohesive production packages and regression
tests, `examples/` reusable examples, `scripts/` deterministic CLI/build/inventory/
packaging checks, `docs/` user/architecture/release documentation, `third_party/`
licenses, and `assessment/` concise current traceability (363 tests, 45 groups).
`.github/` contains CI/release workflows and contribution/issue templates.

Before cleanup, every original workspace file was archived outside the repository
and verified against a per-file SHA-256 manifest. The private archive contains
historical assessments, raw model/audit traces, platform receipts, differential
tools, the isolated Genkit POC, intermediary artifacts and caches. No historical
evidence was discarded. Normal builds/tests need none of it or the Python checkout.
The final private receipt inventory records archive and packaging checksums, source
commit, native environments and execution logs; those environment details are not
public product fixtures. Generated archives, binaries, caches, personal paths and
private traces are excluded from the initial commit.

## Production correction

Validation stdout/stderr capture and per-attempt diagnostic copying are optional.
Loss warns with rerun guidance and records availability/incompleteness plus filtered
bounded previews. Actual process exit status and cleanup errors remain authoritative;
failed checks cannot be accepted. Missing output is not borrowed from an older
attempt. Ten focused cases cover stdout/stderr, exit 0/7, interactive/noninteractive
integration, and per-attempt copy failure. Mandatory proposal/decision evidence,
publication, integrity and before-staging checks remain strict. No dependency,
provider, harness or configuration redesign accompanies this correction.

The unavailable-audit test now selects an explicit temporary non-repository rather
than assuming the project checkout itself is not a Git repository. Help examples
use `git-ez --help`, avoiding Git's native `git ez --help` man-page dispatch.

## Documentation and infrastructure

README, configuration, installation, migration, architecture, CONTRIBUTING and
SECURITY describe the actual product, retired context-byte setting, native Git
continuation, local audit and caller-privilege shell execution. No security contact
or release lifecycle is invented. Permanent Genkit compatibility causes/removal
conditions are recorded in architecture documentation.

[Release engineering](../docs/release-engineering.md) documents CI selection,
least-privilege workflow review, exact tool/action pins, corresponding-source
bundling, verification, offline builds and future hosting steps. Four hosted native
runner jobs plus a FreeBSD amd64 VM are prepared; cross-compilation is separate.
The VM action has a pinned implementation but mutable image/pkg inputs. No actual
hosted workflow execution is claimed before the remote repository exists.
Publication is separate from builds and disabled by default; protected-environment
review and repository settings must be established remotely before enabling it.

The release dry run generates six CGO-disabled binary archives, one source archive,
SHA256SUMS, provenance and exact dependency inventory. Each binary archive includes
its SBOM, licenses and matching complete vendored source plus Go runtime/stdlib
sources. No end-user Python/Go/Node framework runtime is needed.

## Qualification receipts

Native Linux arm64 completed all 15 test packages and the 29 installed CLI
cases. Native FreeBSD arm64 completed the full Go suite and the same 29 cases;
the actual cross-built archive also passed all 14 audit/pager cases there. macOS
arm64 archive rebuilding with empty caches and `GOPROXY=off` reproduced the binary
byte for byte. Corrupted assets and staged-tree publication were explicitly refused.

Executed checks and exact artifact hashes are recorded in the private Phase 8
receipt report alongside this repository. Local checks include formatting/vet,
module integrity/tidy, full deterministic tests, supported native race testing,
installed CLI scripts, actionlint, govulncheck, six-target compilation and binary
inspection, packaging verification, source/vendor offline rebuilding, clean-checkout
qualification and current-build FreeBSD execution. Remote Actions execution remains
unavailable until hosting is explicitly authorized. See the final maintainer report
for measured results and any unavailable checks; this list is not a claim of CI
execution on hypothetical hosted runners.

## Next decisions

Confirm public owner/name/module path and preferred private vulnerability reporting.
Review the open acceptance gates and local receipts. After explicit authorization,
create an empty remote, add its approved URL, push `main`, configure protections and
review actual CI. Version tags and release publication require separate approval.
