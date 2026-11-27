# Contributing

Use Go 1.27+, Git 2.46+ and a supported Unix platform. Release/tool checks pin Go
in `.go-version`. SSH signing tests use `ssh-keygen`; Python 3.12+ is used only by
build/packaging and optional installed-CLI checks. No provider account is needed.

```sh
go mod download
go mod verify
test -z "$(gofmt -l cmd internal)"
go vet ./...
CGO_ENABLED=0 go test -timeout 15m ./...
go test -race -timeout 15m ./...
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
