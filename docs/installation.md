# Installation and upgrades

Git-ez is experimental; successful representative semantic resolution and complete
native platform qualification remain open acceptance requirements. No official
release is published yet. The installer fails clearly until a suitable release is
available. Project releases: <https://github.com/nico97118/git-ez-go/releases>.

## Requirements

Use native Git **2.38 or newer**, a supported Unix OS/CPU and standard platform
utilities. See [platform compatibility](platforms.md) for runtime floors and actual
qualification. Go, Python, Node.js and an agent-framework runtime are not needed
for binary installation or use. Resolving conflicts requires your configured model
endpoint; offline audit consultation does not. Your own validation command may
require additional tools. Prefer a recent Git; see the accepted
[native rebase signoff limitation](git-compatibility.md).

## Recommended installer

```sh
curl -fsSL https://raw.githubusercontent.com/nico97118/git-ez-go/main/install.sh | sh
```

This installs `git-ez` and `git-ez.1` into:

```text
$HOME/.local/bin/git-ez
$HOME/.local/share/man/man1/git-ez.1
```

The script requires a POSIX shell, curl, awk, tar with gzip support, standard Unix
file/text tools and one of `sha256sum`, `shasum` or FreeBSD's `sha256`. It checks Git
but never installs or upgrades it. It uses HTTPS GitHub Release metadata to select
the most recently published stable release. Only if no stable exists does it select
the most recently published pre-release, with a warning. Drafts are excluded.
Selection uses publication time, not lexical version ordering. Only conventional
project tags `vX.Y.Z` and `vX.Y.Z-PRERELEASE` are eligible.

Select exactly a published version, including an older release or pre-release:

```sh
curl -fsSL https://raw.githubusercontent.com/nico97118/git-ez-go/main/install.sh | sh -s -- --version v0.1.0-alpha.1
```

For a custom prefix, download the script and run it directly:

```sh
curl -fsSL -o install.sh https://raw.githubusercontent.com/nico97118/git-ez-go/main/install.sh
less install.sh
sh install.sh --prefix /usr/local
```

The last command needs permission to write that prefix. There is no automatic sudo
and no interactive confirmation. `sh install.sh --help` lists the two options.
The equivalent piped invocation is `sh -s -- --prefix /chosen/prefix` after the curl
command. You can pin the installer itself to a release tag instead of `main`:

```sh
curl -fsSL https://raw.githubusercontent.com/nico97118/git-ez-go/v0.1.0-alpha.1/install.sh | sh -s -- --version v0.1.0-alpha.1
```

That example requires the tag/release to exist. Pinning the script does not itself
pin the selected binary version: supply `--version` when both should be fixed.

The installer resolves one release, checks required asset names (including the separate source asset) and downloads only
that target's archive and its `SHA256SUMS`. It verifies the checksum before inspecting
or extracting the archive, rejects traversal, links, special files and duplicate
paths, checks required files, and runs the prepared executable's exact `--version`.
It does not download source during binary installation.

Private temporary storage is cleaned on success, failure and catchable interruption.
The complete installer body and its final entry-point guard must arrive before
installation can begin. Downloading and inspecting the script is preferable when
you want to review code before execution. No shell program can clean up after
SIGKILL or power loss; leftover private temporary files can be removed manually.

Archive checksums detect corruption but do **not** independently authenticate a
compromised GitHub account, release or installer. Obtain installer and artifacts
through a trusted source. Credentials are not required for this public repository;
GitHub API rate limits or download errors cause an explicit failure, with manual
installation as the alternative.

## PATH, manual discovery and verification

The installer changes neither PATH nor MANPATH, startup files, Git configuration,
credentials nor audit data. If needed, set these in your current shell yourself:

```sh
export PATH="$HOME/.local/bin:$PATH"
git ez --version
MANPATH="$HOME/.local/share/man:" git ez --help
```

The trailing MANPATH colon preserves system defaults. Persist these settings only
if desired and if your platform does not already search those directories.
`git-ez --help` and `git-ez COMMAND --help` work with the binary alone; conventional
`git ez --help` uses the installed manual. The installer verifies the executable
through its direct destination path even if that directory is not on PATH.

## Upgrades, downgrades and existing files

Run the same installer again for the latest selected release. Use `--version` for
an exact upgrade or downgrade. Configuration and journals remain in their existing
Git/user locations. There is no in-program self-update command.

Downloads, checksum checking, extraction and executable verification finish before
replacement begins. Both files are prepared on their destination filesystems;
the manual is renamed first, then the executable is atomically renamed. These
are separate file replacements, not a multi-file transaction: an exceptional
failure at final binary publication can leave the new manual with the old binary.
An existing running process can continue using its old executable; wait for active
integrations to finish before changing versions.

The installer refuses a symlink, directory, non-executable or unidentifiable file
at `PREFIX/bin/git-ez`. A regular executable is recognized only by a successful
`--version` response starting with `git-ez `; this convention is a heuristic, not
proof of provenance. It does not identify implementation languages or migrate
installations. Inspect unexpected destinations yourself and use manual installation.
There is no force-overwrite option. Manual-page symlinks/directories are also
refused. Other installations earlier on PATH are not managed by this script.

## Manual installation, including offline use

Manual installation is fully supported. Select **one** OS/architecture archive:
`linux_amd64`, `linux_arm64`, `freebsd_amd64`, `freebsd_arm64`, `darwin_amd64` or
`darwin_arm64`. Darwin means macOS. The archive is per OS/CPU, not per OS release.

1. From the selected GitHub Release, download `git-ez_VERSION_TARGET.tar.gz` and
   that release's `SHA256SUMS`. Transfer both on trusted media for an offline host.
2. Verify the archive digest against exactly one matching filename in SHA256SUMS.
   For example, substituting an actual published version and your target:

   ```sh
   version=v0.1.0-alpha.1
   target=linux_amd64
   archive="git-ez_${version}_${target}.tar.gz"
   sha256sum "$archive"       # Linux
   # shasum -a 256 "$archive" # macOS
   # sha256 -q "$archive"     # FreeBSD
   grep -F "  $archive" SHA256SUMS
   ```

   Compare the full 64-character digest and exact filename; stop if absent,
   duplicated or different. This is an explicit comparison, not an automatic
   verification by the grep command. Authenticate both files through a trusted
   channel. The checksum command itself needs no network access.
3. Inspect/extract the verified official archive into an empty directory:

   ```sh
   tar -tzf "$archive"
   mkdir unpacked
   tar -xzf "$archive" -C unpacked
   package="unpacked/git-ez_${version}_${target}"
   ```

4. Install both files, user-local by default:

   ```sh
   prefix="$HOME/.local"
   mkdir -p "$prefix/bin" "$prefix/share/man/man1"
   install -m 755 "$package/git-ez" "$prefix/bin/git-ez"
   install -m 644 "$package/docs/man/git-ez.1" "$prefix/share/man/man1/git-ez.1"
   "$prefix/bin/git-ez" --version
   export PATH="$prefix/bin:$PATH"
   git ez --version
   MANPATH="$prefix/share/man:" git ez --help
   ```

   For system-wide installation set `prefix=/usr/local` and run the installation
   file operations with appropriate administrator permissions. Inspect existing
   files before replacing them. Manual copy/install commands do not implement the
   installer's prepared atomic replacement; avoid active integrations and retain
   the old executable until the replacement is verified.

All required executable/manual files, licenses, third-party notices, SBOM and build
metadata are in the binary archive. Offline installation needs no API access,
source archive, language toolchain or agent-framework download.

## Source and reproducible rebuilding

The matching `git-ez_VERSION_source.tar.gz` is a separate complete corresponding-source
asset in the **same GitHub Release**, as described by `SOURCE.txt` and `BUILD.json`.
Verify its digest against authenticated release checksums. GitHub's automatically
generated source archives are not substitutes: the complete archive contains
vendored modules, inventories and exact Go runtime sources.

To rebuild offline, transfer that source archive and its recorded exact Go SDK.
From `git-ez-source/`, run this as one shell command line, substituting the recorded
version:

```sh
CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=vendor -buildvcs=false -trimpath -ldflags "-s -w -buildid= -X main.version=VERSION" -o git-ez ./cmd/git-ez
```

Production binaries omit linker symbols/DWARF. Rebuild without `-s -w` for debugging;
use `-gcflags=all="-N -l"` when needed. Such a debugging binary intentionally differs
from the production binary. See [release engineering](release-engineering.md).

## Uninstallation

Remove only the installed executable and manual page (adjust the prefix if needed):

```sh
prefix="$HOME/.local"
rm "$prefix/bin/git-ez" "$prefix/share/man/man1/git-ez.1"
```

This preserves all Git configuration, credentials and repository audit data. Remove
any shell search-path settings you added yourself only if you no longer need them.
