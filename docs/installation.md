# Installation and upgrades

No official release is published yet. Future archives contain one native `git-ez`
executable, documentation/licenses, inventories, an SBOM and its matching source
bundle. Select Linux/FreeBSD/macOS and amd64/arm64 explicitly. Verify the trusted
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
A release's source bundle includes vendored modules for `go build -mod=vendor`
without dependency downloads. Use its recorded exact Go toolchain and build flags
for independently verifiable output. Git/source authentication remains separate
from checking the integrity of downloaded archive bytes.
