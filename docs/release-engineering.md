# Release engineering

No public release or remote repository has been created by the local preparation.
The current module path `github.com/nico97118/git-ez-go` is retained. The maintainer
must confirm the public repository name and module path before hosting; changing
it requires updating imports and requalification, not merely adding a remote.

## Local packaging

Use Python 3.12+, native Git 2.46+ and the exact Go SDK in `.go-version`.
Run from a committed checkout:

```sh
python3 scripts/release.py --revision HEAD --output /tmp/git-ez-release
python3 scripts/verify-release.py /tmp/git-ez-release
```

The output directory must be empty. Untagged commits produce development versions;
exactly one semver `vMAJOR.MINOR.PATCH[-prerelease]` tag selects a release version.
`--snapshot` packages only the explicitly staged tree and cannot be published.
No command here creates tags or publishes anything. Version selection, commit/tree,
Go SDK, build flags, hashes and unsigned provenance are recorded. Checksums detect
corruption, not authenticity; obtain them from a trusted channel.

The stdlib Python scripts are preferred over GoReleaser: six archives, checksums,
source bundling and inventories need no additional packaging dependency. Source
selection uses `git archive`; unrelated working-tree changes are excluded.
Builds disable CGO and VCS auto-stamping, use trimpath and an empty build ID.
Tar/gzip ordering, timestamps, permissions and ownership are normalized. Exact
byte reproducibility depends on the same source, SDK and provenance environment;
a second independently built matching manifest is evidence, not a blanket promise.

Each of six archives contains the executable, documentation, complete GPL license,
NOTICE, third-party license texts, per-target CycloneDX SBOM, exact dependency
inventory, BUILD metadata and a complete corresponding `SOURCE.tar.gz`.
The same source bundle is also a separate seventh archive. It includes the selected
project tree, vendored modules, exact Go standard-library/runtime sources, upstream
licenses, build scripts and a SHA-256 source manifest (excluding itself). The SDK
compiler, native Git, operating system and model weights are not redistributed.
Rebuild with the matching SDK and `GOPROXY=off go build -mod=vendor ./cmd/git-ez`.
Source is delivered with each binary; no time-limited written source offer is used.

## CI choices and boundaries

`ci.yml` runs deterministic formatting, vet, module verification/tidy, unit and
integration tests, scripted native-provider fixtures, race detection where supported,
and installed CLI review/audit/signal tests. Linux amd64/arm64 and macOS amd64/arm64
use native hosted runners. A separate job cross-compiles all six targets and runs
pinned actionlint and govulncheck. Dependency caching is an acceleration only;
`go.sum` verification remains required. Real-model inference is never a CI step.

FreeBSD options considered: Cirrus CI introduces another hosted service/account;
a self-hosted runner requires maintenance and is unsuitable for untrusted PRs;
a QEMU FreeBSD VM on a hosted Linux runner provides a native FreeBSD kernel without
new paid infrastructure. The pinned MIT-licensed `vmactions/freebsd-vm` action is
selected, with non-root tests and copyback disabled. Its VM image and pkg repository
are not content-addressed here: OS/package supply-chain inputs remain mutable.
A version guard fails if installed Git is below 2.46 or Go cannot build the module.
This is qualification, not a hermetic build claim. FreeBSD arm64 is covered by the
available maintainer VM, not this amd64 CI job. Race detection is unavailable there.
Runner labels/platforms must be reviewed when GitHub retires images.
See [runner inventory](https://github.com/actions/runner-images) and
[FreeBSD action](https://github.com/vmactions/freebsd-vm).

## Workflow security and publication

All actions are pinned by full commit SHA; review upstream source/license and SHA
updates together. Build/test tokens have only `contents: read`, checkout does not
persist credentials, PRs run on disposable hosted environments, and no provider
or publication secrets are passed to builds. No `pull_request_target` or privileged
`workflow_run` pattern is used. Actionlint v1.7.12 (MIT) and govulncheck v1.10.0
(BSD-3-Clause) are development tools, not production dependencies. Vulnerability
results are dated observations of the Go database, not a guarantee of absence.

`release.yml` supports manual packaging of a selected revision without publication.
Tag pushes prepare artifacts in a read-only job. Publication requires all of:
`RELEASE_PUBLICATION_ENABLED=true`, a `v*` tag event, and approval of the protected
`release` environment. Only that separate job has `contents: write`. It checks out
the trusted default branch, verifies every artifact checksum and embedded binary /
source hash, verifies the tag's immutable commit against provenance and build output,
and requires that commit to be an ancestor of the checked-out default branch.
Only the final `gh release create --verify-tag` step receives the publication token.
Artifacts from this workflow run are used; no arbitrary user artifact URL is accepted.

These protections require remote settings: configure `release` required reviewers,
prevent self-review, restrict deployment tags and withhold the enable variable until
reviewed. Workflow YAML alone cannot enforce environment reviewers. Keep default
workflow token permissions read-only, disable Actions creating/approving PRs, enable
private vulnerability reporting if desired, and protect `main` with review and
required status checks: all four `Native (...)` jobs, `Native (FreeBSD amd64 VM)`,
and `Workflows, dependencies and six-target builds`. Require review of workflow
changes. Do not enable publication before these settings exist. No settings were
changed remotely. See GitHub's [secure-use guidance](https://github.com/github/docs/blob/main/content/actions/reference/security/secure-use.md)
and [workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax).

## Future hosting, only after maintainer authorization

1. Confirm the repository/module name, license/notice inventory, security reporting
   channel and open acceptance gates in `assessment/readiness.md`.
2. Create an empty GitHub repository under the explicitly approved owner/name;
   do not initialize a competing README/license/history remotely.
3. Add the approved URL with `git remote add origin APPROVED_URL`, verify it using
   `git remote -v`, then explicitly authorize `git push -u origin main`.
4. Configure branch protection, Actions permissions and the protected environment
   above. Review the first actual hosted CI run; local checks do not prove hosted CI.
5. Run manual release engineering on the approved commit and inspect downloaded
   archives on the intended platforms. Close material acceptance gates before release.
6. Only with separate release authorization, create/review a version tag, enable
   protected publication, and push that tag. No part of Phase 8 performs these steps.

For local native archive qualification, run
`python3 scripts/check-package.py /tmp/git-ez-release --evidence /tmp/package-check.json`.
It verifies all assets, tests Git extension discovery, then rebuilds the native
binary from the embedded source with empty module/build caches and dependency
network access disabled. The rebuilt binary must match byte for byte.
