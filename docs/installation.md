# Installation and upgrades

No official release is published yet. Future archives contain one native `git-ez`
executable, documentation/licenses, inventories, an SBOM and a `SOURCE.txt` reference
to the matching separate complete source archive. Select Linux/FreeBSD/macOS and amd64/arm64 explicitly. Verify the trusted
SHA-256 checksum before extraction; inspect `BUILD.json` for source/toolchain identity.

Install `git-ez` into an executable directory on PATH, for example `~/.local/bin`.
`command -v git-ez` and `git ez --version` verify discovery. Native Git 2.46+ and
the configured model service are the only ordinary runtime prerequisites; your
own validation program may of course require its language/runtime.

For offline installation, transfer the archive and authenticated checksum data on
trusted media, verify on the destination and install the executable. There is no
bootstrap download, custom installer, package-manager dependency or framework
runtime. Offline audit consultation works independently of the model endpoint.

Upgrade by checking for active git-ez processes, verifying the replacement archive,
and replacing the executable. Check `command -v git-ez` for competing Python/Go
installations before invoking Git's extension discovery. Configuration and audit
storage belong to Git/the working copy and are not installed into the executable.
Remove the executable to uninstall; retain audit data until you decide it is no
longer needed. Never automatically delete repository configuration or journals.

For source builds, install Go, verify modules and use the README build command.
Download the matching `git-ez_VERSION_source.tar.gz` asset from the same GitHub
Release, verify its SHA-256 against `SOURCE.txt`/`BUILD.json` and trusted checksums,
and extract it separately. GitHub's automatically generated source archives do not
include vendored modules or exact Go runtime sources and are not substitutes.
The complete source archive includes vendored modules for `go build -mod=vendor`
without dependency downloads. Use its recorded exact Go toolchain and build flags
for independently verifiable output. Git/source authentication remains separate
from checking the integrity of downloaded archive bytes.

Binary installation remains offline and requires only the binary archive, native
Git and host OS; downloading source is optional for installation. For an offline
rebuild, transfer the separate complete source archive and recorded Go SDK. Use
`GOTOOLCHAIN=local GOPROXY=off go build -mod=vendor -buildvcs=false -trimpath
-ldflags "-s -w -buildid= -X main.version=VERSION" -o git-ez ./cmd/git-ez`
from `git-ez-source/` (on one shell command line), substituting the exact recorded
version. Standard distribution binaries omit linker symbols/DWARF. For interactive
debugging, rebuild without `-s -w`; use `-gcflags=all="-N -l"` when needed. This
debugging rebuild is intentionally different from the reproducible production binary.
