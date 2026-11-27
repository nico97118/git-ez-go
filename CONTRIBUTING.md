# Contributing

Use Go 1.27+, Git 2.38+ and a supported Unix platform. Release/tool checks pin Go
in `.go-version`. Normal Go tests use native Git, a POSIX shell, `awk` and standard Unix utilities.
See [Git compatibility](docs/git-compatibility.md) for minimum-version qualification
and the accepted native signoff limitation. SSH signing tests use `ssh-keygen`; Python 3.12+ is used only by
build/packaging and optional installed-CLI checks. No provider account is needed.

```sh
go mod download
go mod verify
go mod tidy -diff
test -z "$(gofmt -l cmd internal scripts)"
go vet ./...
CGO_ENABLED=0 go test -timeout 15m ./...
CGO_ENABLED=1 go test -race -timeout 20m ./...
python3 scripts/build.py --output /tmp/git-ez-builds --evidence /tmp/git-ez-builds.json
```

Race testing is supported on the configured Linux/macOS runners and FreeBSD amd64;
FreeBSD arm64 is not supported by the Go race detector. Cross-compilation is a
separate check. Normal Go tests use temporary Git repositories, self-process signal
helpers, deterministic resolvers and local scripted HTTP/TLS providers. They do not
invoke a real model, require credentials or modify global Git settings.

Add regressions for observable behavior, using real Git for Git semantics and
scripted providers for protocol behavior. Preserve the framework-independent resolver
boundary. Test stdout/stderr failures independently from mandatory evidence loss;
never permit acceptance after failed checks or restore files while cleanup is uncertain.
Use gofmt and explicit contexts/errors. Do not add a dependency without a concrete
purpose and license review. Do not translate implementation-specific Python tests
mechanically. Local migration evidence is not a prerequisite for contributing.
See the [qualification status](docs/qualification.md) for open acceptance requirements.

Installed CLI checks accept explicit binary/evidence paths:

```sh
python3 scripts/check-interactive.py --binary /tmp/git-ez-builds/git-ez-linux-amd64 --evidence /tmp/interactive.json
python3 scripts/check-audit.py --binary /tmp/git-ez-builds/git-ez-linux-amd64 --evidence /tmp/audit.json
python3 scripts/check-signal.py --binary /tmp/git-ez-builds/git-ez-linux-amd64 --evidence /tmp/signals.json
python3 scripts/check-paths.py --binary /tmp/git-ez-builds/git-ez-linux-amd64 --evidence /tmp/paths.json
```

Use the binary matching the actual host. These are deterministic local fixtures.
Real-model evaluations are explicitly opt-in, separate from normal tests, CI and
release builds, and require an intentional endpoint/cost decision. Do not repeat
weak-model experiments merely to obtain a pass. Successful semantic resolution
with a sufficiently capable model remains open. Historical evaluation tools and
private traces are archived outside this repository, not product dependencies.

Prepare changes with their behavior, rationale, regression results and limitations.
Never include credentials, private repositories/model traces, local environment
paths or generated binaries. [Architecture](docs/architecture.md),
[release engineering](docs/release-engineering.md), [security](SECURITY.md).

Installer regression tests run with `go test ./scripts` (also included in `./...`).
They use local release fixtures and real shell/archive/checksum tools, with mocked
GitHub transport; no network or published releases are required. Native installer
qualification is explicitly enabled by `GIT_EZ_INSTALL_TEST_BINARY` pointing to a
matching binary built with `-ldflags "-X main.version=v0.0.0-installer-test"`.
That check additionally requires `man` and the platform manual formatter. Existing
native CI runs it after building its smoke-test executable.

## CI and local checks

`sh scripts/ci.sh` runs complete local qualification: static checks, native vet,
one complete normal Go suite, one complete supported race suite, and installed
CLI/native installer checks. `sh scripts/ci.sh --native` skips repository-wide
static checks for the FreeBSD VM. Normal hosted runners execute the checks directly
as named Actions steps. Go retains package-level parallelism and automatic discovery;
there is no test inventory or qualification profile to update for new tests/packages.

Use normal Go commands for focused debugging, for example:

```sh
CGO_ENABLED=0 go test -v ./internal/agent ./internal/resolver
CGO_ENABLED=0 go test -v ./internal/git ./internal/engine ./internal/conflictstate
```

Before committing, run formatting, module verify/tidy, native vet and complete
normal/race tests as above. Run the shared build utility for platform/process/build
changes. Workflow changes also require pinned actionlint and shell/Python syntax
checks. The local convenience script does not run actionlint, govulncheck or
cross-builds; CI runs them separately. No tracked development hooks are installed.
See [CI organization](docs/ci.md) for job/platform coverage and diagnostics, and
[release engineering](docs/release-engineering.md) for packaging and the complete
native qualification gate on the exact packaged commit before publication.
