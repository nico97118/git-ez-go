# Release engineering

No public release or remote repository has been created by the local preparation.
The current module path `github.com/nico97118/git-ez-go` is retained. The maintainer
must confirm the public repository name and module path before hosting; changing
it requires updating imports and requalification, not merely adding a remote.

## Local packaging

Use Python 3.12+, native Git 2.38+ and the exact Go SDK in `.go-version`.
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
Production builds disable CGO and VCS auto-stamping, use trimpath, an empty build ID
and linker `-s -w`. Development builds keep their default debug information.
`-s` omits the symbol table and debug information (and implies `-w`); the explicit
pair documents the distribution policy. Runtime stack traces remain available,
but symbol/debugger inspection of the shipped binary is limited. Rebuild the exact
source without these flags for debugging; no extra debug archive is published.
See the [Go linker documentation](https://pkg.go.dev/cmd/link) and
[debugging guidance](https://go.dev/doc/gdb).
Tar/gzip ordering, timestamps, permissions and ownership are normalized. Exact
byte reproducibility depends on the same source, SDK and provenance environment;
a second independently built matching manifest is evidence, not a blanket promise.

Each of six archives contains the executable, documentation, complete GPL license,
NOTICE, third-party license texts, per-target CycloneDX SBOM, exact dependency
inventory, BUILD metadata and `SOURCE.txt` identifying the separate complete source
archive by filename, SHA-256 and (for published tags) a direct release download URL.
There is one complete source archive per release, the seventh archive overall. It includes the selected
project tree, vendored modules, exact Go standard-library/runtime sources, upstream
licenses, build scripts and a SHA-256 source manifest (excluding itself). The SDK
compiler, native Git, operating system and model weights are not redistributed.
Rebuild with the matching SDK and `GOPROXY=off go build -mod=vendor ./cmd/git-ez`.
Source and all six binaries must be offered together in the same GitHub Release
Assets, with equivalent access at no additional charge, under GPL-3.0 section 6(d).
Recipients need not download source to install the binary. Maintain source access
for as long as needed to satisfy that provision, including after binary mirroring.
No time-limited written source offer or automatically generated GitHub source archive
is substituted for the complete archive. See [GPL section 6](https://www.gnu.org/licenses/gpl-3.0.html#section6).

## CI choices and boundaries

`ci.yml` runs deterministic formatting, vet, module verification/tidy, unit and
integration tests, scripted native-provider fixtures, race detection where supported,
and installed CLI review/audit/signal/path tests. Complete normal/race suites run
on each configured native platform, with Go's package-level parallelism.
Linux amd64/arm64 and macOS amd64/arm64 use native hosted runners. Separate quality
and dependency jobs run pinned actionlint and govulncheck;
a build job exposes all six targets as named steps. See the
[CI organization](ci.md). Dependency caching is an acceleration only;
`go.sum` verification remains required. Real-model inference is never a CI step.

FreeBSD options considered: Cirrus CI introduces another hosted service/account;
a self-hosted runner requires maintenance and is unsuitable for untrusted PRs;
a QEMU FreeBSD VM on a hosted Linux runner provides a native FreeBSD kernel without
new paid infrastructure. The pinned MIT-licensed `vmactions/freebsd-vm` action is
selected, with non-root tests and copyback disabled. Its VM image and pkg repository
are not content-addressed here: OS/package supply-chain inputs remain mutable.
A version guard fails if installed Git is below 2.38 or Go cannot build the module.
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
Tag pushes prepare artifacts in a read-only job. After packaging, the same CI
workflow runs complete native qualification on the exact packaged commit; publication
depends on both jobs succeeding. Manual release preparation also includes this
complete native qualification. Publication requires all of:
`RELEASE_PUBLICATION_ENABLED=true`, a `v*` tag event, and approval of the protected
`release` environment. Only that separate job has `contents: write`. It checks out
the trusted default branch, verifies every artifact checksum, binary hash and the separate
source archive association, verifies the tag's immutable commit against provenance and build output,
and requires that commit to be an ancestor of the checked-out default branch.
Publication first creates a draft with all assets, downloads/verifies that draft
and compares its checksums with the qualified files, then makes it public. A failed
upload/verification leaves a draft for maintainer inspection, never a public partial
release. The publication token is limited to those final upload/verification steps.
Artifacts from this workflow run are used; no arbitrary user artifact URL is accepted.

These protections require remote settings: configure `release` required reviewers,
prevent self-review, restrict deployment tags and withhold the enable variable until
reviewed. Workflow YAML alone cannot enforce environment reviewers. Keep default
workflow token permissions read-only, disable Actions creating/approving PRs, enable
private vulnerability reporting if desired, and protect `main` with review and
required status checks: all four `Native (...)` jobs, `Native (FreeBSD amd64 VM)`,
plus `Code quality and workflow syntax`, `Dependency integrity and vulnerabilities`,
and `Six production compilation targets (CGO disabled)`. Require review of workflow
changes. Do not enable publication before these settings exist. No settings were
changed remotely. See GitHub's [secure-use guidance](https://github.com/github/docs/blob/main/content/actions/reference/security/secure-use.md)
and [workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax).

## Future hosting, only after maintainer authorization

1. Confirm the repository/module name, license/notice inventory, security reporting
   channel and open acceptance gates in `docs/qualification.md`.
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
binary from the separate complete source archive with empty module/build caches and dependency
network access disabled. The rebuilt binary must match byte for byte.
