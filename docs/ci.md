# CI organization

GitHub Actions owns hosted orchestration. Go discovers and runs tests using its
normal package parallelism. There is no package inventory, test selector or
standard/extended profile to maintain.

## Jobs and meaningful steps

| Job | Checks |
|---|---|
| Code quality and workflow syntax | Formatting, module tidy, pinned actionlint, shell/Python syntax |
| Dependency integrity and vulnerabilities | Module verification and pinned govulncheck |
| Native Linux/macOS matrix | Git/toolchain verification, native vet, one complete verbose Go test invocation, one complete verbose race invocation, native CLI build, four focused CLI scenarios and native installer/manual qualification |
| Native FreeBSD amd64 VM | Same native checks, grouped inside a single VM invocation as a non-root user with copyback disabled |
| Six production compilation targets | Six named steps using the shared production build policy in `scripts/build.py` |

Repository-wide formatting and module checks run once. Native vet stays on every
platform because production source selection differs by OS. Native runners remain
Linux amd64/arm64, macOS arm64/Intel and the FreeBSD 15.1 amd64 VM. Existing runner
labels, timeout budgets, SHA pins, read-only permissions and failure diagnostics
are retained. Matrices use `fail-fast: false`.

Each native platform runs:

```sh
CGO_ENABLED=0 go test -v -timeout 15m ./...
CGO_ENABLED=1 go test -race -v -timeout 20m ./...
```

These include deterministic providers, Git/engine, audit/journal, process/safety,
CLI/configuration and installer fixtures. New packages and tests are automatically
discovered by Go, including applicable platform-specific files. Verbose output
shows individual tests/subtests and package durations; Actions shows step durations
and failures. Independent race/CLI steps can execute after a failed normal suite
without concealing that failure. No extra runners compensate for serialized tests.

The native binary installer test is intentionally opt-in and also runs separately
with `GIT_EZ_INSTALL_TEST_BINARY` pointing to the freshly built installer-version
binary. The four focused scripts remain individually named steps:
`check-interactive.py`, `check-audit.py`, `check-signal.py` and `check-paths.py`.
Available CLI JSON diagnostics are retained on failure for seven days. FreeBSD
keeps grouped logs and disabled copyback. FreeBSD arm64 qualification remains
available locally; only its unsupported race detector is explicitly skipped.

## Why the selector and profiles were removed

The former standard normal profile saved only three pure tests on each secondary
platform, while its six family steps prevented Go from running agent and engine
packages concurrently. Focused race selection saved some fixture work but required
an inventory, exemption policy, profile inputs and dedicated regression tests.
The maintenance cost and lost normal-test parallelism outweighed those savings.

All native environments now use complete normal/race suites. This restores broad
qualification and conventional Go discovery without custom coverage accounting.
No production or Go test source changed. No measured hosted speedup is claimed:
compare actual hosted timings after this simplification, especially Intel macOS
and the VM. Existing 35-minute native, 50-minute Intel and 40-minute VM budgets stay.

## Local checks and focused debugging

`sh scripts/ci.sh` runs static checks, native vet, complete normal/race suites and
all installed CLI/native installer checks. `sh scripts/ci.sh --native` omits only
repository-wide static checks for the VM. It groups logs, continues independent
checks and returns the first failing command status. It never schedules individual
Go packages. Actionlint, govulncheck and cross-builds remain separate commands.

Use ordinary Go package commands to investigate a failure, for example:

```sh
CGO_ENABLED=0 go test -v ./internal/agent ./internal/resolver
CGO_ENABLED=0 go test -v ./internal/git ./internal/engine ./internal/conflictstate
CGO_ENABLED=0 go test -v ./internal/audit ./internal/journal
CGO_ENABLED=0 go test -v ./internal/process ./internal/locking ./internal/application
CGO_ENABLED=0 go test -v ./scripts
```

These are optional debugging selections, not a CI partition. Use `-run` for one
regression and `-count=1` to intentionally bypass valid Go test result caching.
Reproduce native failures with the corresponding OS/CPU, Git and SDK. Containers
need an init/subreaper for descendant cleanup tests, and native manual qualification
requires `man` plus its formatter. Missing prerequisites must fail qualification.
See [CONTRIBUTING](../CONTRIBUTING.md). No tracked development hooks are added.

## Release gates and hosted validation

Manual CI dispatch runs the same complete qualification as pushes/PRs. Release
engineering retains packaging, source/SBOM delivery, integrity/tamper checks and
byte-identical offline reconstruction. It calls this same reusable CI workflow
with the exact packaged commit before publication; there is no weaker profile.
Publication still depends on both packaging and qualification, then on the existing
tag, enable variable, protected environment, provenance/ancestry, draft upload and
downloaded-asset verification gates. No secrets are passed to qualification.

The eight ordinary CI status names remain unchanged; no branch-protection changes
are required relative to the preceding refactor. The release qualification display
name now says “Complete” instead of “Extended”; its dependency remains mandatory.
No remote settings are changed. Hosted amd64 execution, reusable-workflow runtime,
artifact handling and runner timings must still be confirmed on GitHub. Local
verification and cross-compilation do not establish those hosted results.
