# git-ez

AI-assisted Git integration, implemented in Go and licensed GPL-3.0-only.
Git performs merges, rebases and cherry-picks. When eligible text conflicts remain,
git-ez asks a configured model to investigate the repository and propose edits.
It independently checks the proposal, optionally runs your validation command,
and records the additional LLM activity in a local audit journal.

This is an integration tool, not a replacement for Git or a generic agent framework.
Model output can be wrong. Semantic resolution quality remains an open acceptance
requirement; complete Python parity and unconditional production readiness are not claimed.

## Install

Runtime requirements: **Git 2.46 or newer**, a supported operating system and access
to your configured model endpoint. Python, Go, Node.js and Genkit are not required
on the end user's machine. No official release has been published yet.

When authorized release archives are available, obtain the archive for your OS and
architecture and its `SHA256SUMS` through a trusted channel. Verify before extracting:

```sh
sha256sum git-ez_VERSION_linux_amd64.tar.gz
# Compare the digest with its entry in the trusted SHA256SUMS.
# macOS: shasum -a 256 ARCHIVE; FreeBSD: sha256 ARCHIVE
mkdir unpacked
tar -xzf git-ez_VERSION_linux_amd64.tar.gz -C unpacked
mkdir -p "$HOME/.local/bin"
install -m 755 unpacked/git-ez_VERSION_linux_amd64/git-ez "$HOME/.local/bin/git-ez"
export PATH="$HOME/.local/bin:$PATH"
git ez --version
```

Replace `VERSION` and the target with the downloaded archive's actual names.
For FreeBSD's base tools use `sha256 ARCHIVE` and compare its digest with the
trusted checksum file. Checksums detect changes; obtain both binaries and checksums
from an authenticated source. Archives include licenses and the matching complete
source bundle. [Offline installation, upgrades and removal](docs/installation.md).

## Quick start

Configuration uses native Git settings, including normal local/global precedence.
Configure a provider/model before starting an integration. Example for local Ollama:

```sh
git config ez.provider ollama
git config ez.model qwen3:8b
git config ez.endpoint http://localhost:11434/api/chat
git config ez.modelContextTokens 32768
git config ez.modelMaxOutputTokens 8192
git config ez.requestOptions '{"num_ctx":32768,"reasoning":true}'
git ez probe
```

Use a model capable of the task. Qwen3 8B was useful for interoperability and bug
detection; it did not establish successful semantic resolution. Match Ollama's
actual context capacity to the declared model capacity. Local inference is optional.

An OpenAI-compatible Chat Completions endpoint uses the native compatible protocol:

```sh
git config ez.provider openai
git config ez.model YOUR_MODEL
git config ez.endpoint https://YOUR_SERVER/v1/chat/completions
git config ez.apiKeyEnv MY_MODEL_TOKEN
# Set MY_MODEL_TOKEN privately in your environment; do not put secrets in commits.
git ez probe
```

`probe` invokes the configured model and can incur provider charges. The Responses
API is not supported. Both providers support explicit CA bundles and intentional
transport settings; see the [configuration reference](docs/configuration.md).

```sh
git ez merge main --interactive --check go test ./...
git ez rebase main --interactive --check go test ./...
git ez cherry-pick COMMIT --interactive --check go test ./...
```

The arguments following `--check` are a literal executable/argument vector, not
integration options. Interactive review covers the whole proposal: accept,
reject/reprompt with feedback, or quit. Failed validation cannot be accepted;
validation feedback can trigger another attempt. Rejection and cancellation restore
the original tracked conflict state when process cleanup is confirmed. If cleanup
is uncertain, git-ez preserves recovery data and requires manual intervention.

Without `--interactive`, final validation can fail after native Git has completed;
the command reports failure and retains the actual native result. Missing optional
validation output causes a warning, not a false check failure. Mandatory proposal
evidence and integrity checks still precede staging.

Native Git retains ownership of recovery and continuation:

```sh
# Resolve/stage manually when appropriate, then continue through native Git:
git rebase --continue
# Or let git-ez delegate continuation and handle new eligible stops:
git ez rebase --continue
```

`git-ez --help` and each command's help show supported options. Arbitrary Git option
forwarding, automatic fetch/push/stash and automatic worktree creation are not provided.

## Audit and trust

```sh
git ez audit list
git ez audit show
git ez audit show --verbose
git ez audit show --path /path/to/copied/run --verbose
```

Consultation is offline and needs no provider credentials. Native Git pager
conventions apply. Journals explain model requests, tools, proposals, checks,
review and application status; they do not duplicate native Git command streams.
Model-free operations do not create persistent LLM audit runs.

**LLM shell commands run with your privileges and are not OS-sandboxed.** Use trusted
repositories and endpoints, inspect proposed changes and validate independently.
Repository contents may be transmitted to the configured endpoint. Git hooks,
filters, editors and validation programs retain their native privileges.
See [security assumptions](SECURITY.md) and [architecture](docs/architecture.md).

## Build and contribute

```sh
go mod download
go mod verify
CGO_ENABLED=0 go build -trimpath -o bin/git-ez ./cmd/git-ez
```

Go 1.27 or newer is required; release engineering pins its exact toolchain separately.
See [development/testing](CONTRIBUTING.md) and [release engineering](docs/release-engineering.md).
Six targets are built: Linux, FreeBSD and macOS, each amd64 and arm64. Local native
qualification covers arm64 on all three OSes; amd64 execution awaits CI or native
qualification. Compilation and workflow definitions are not evidence of native execution.
[Current acceptance gaps](assessment/readiness.md).

The Go implementation is based on the Python git-ez functional reference pinned to
`69693f61ea5a1d668f103eb3dec0309efde08dbe`. [Python migration](docs/migration.md),
[attribution and third-party terms](NOTICE), [GPL-3.0-only license](LICENSE).
