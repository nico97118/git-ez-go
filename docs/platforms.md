# Platform compatibility and qualification policy

Git-ez ships **six** CGO-disabled executables: Linux, FreeBSD and macOS, each for
amd64 and arm64. One executable covers an OS/CPU combination; OS releases do not
have separate assets. Native **Git 2.46+** is required on every platform. The
installer does not supply Git, emulate another CPU or upgrade the operating system.

## Runtime expectations, not certification

The pinned release SDK is Go **1.27.4** (`.go-version`). Build flags explicitly use
`GOAMD64=v1`, `GOARM64=v8.0` and `CGO_ENABLED=0`.

| Target | Upstream/runtime expectation | Qualification limit |
|---|---|---|
| Linux amd64 | Go's Linux kernel floor is 3.2; x86-64 v1 baseline, no AVX requirement. Static ELF, no glibc dependency from git-ez. | Such an old kernel is not certified by current tests. Native Git and host tools have their own requirements. |
| Linux arm64 | ARMv8.0-A, using the Linux arm64 kernel ABI; the general Go Linux floor does not imply that every architecture existed on kernel 3.2. Static ELF. | Current Linux arm64 execution does not certify old kernels, embedded configurations or user-space emulators. |
| FreeBSD amd64 / arm64 | Go's port matrix lists 12-STABLE and later branches. Native syscall/kernel ABI and standard system facilities are required; ELF is statically linked. | No oldest FreeBSD release is yet certified for git-ez. Upstream Go port support is not a claim of git-ez qualification or continued FreeBSD security support. Prefer maintained releases. |
| macOS amd64 / arm64 | Go 1.27 requires **macOS 13 Ventura or later**; amd64 v1 / ARMv8.0 CPU baselines. System libraries remain runtime prerequisites even with CGO disabled. | The minimum macOS release has not been exercised. A binary built on a newer host is not evidence of execution on macOS 13. |

Sources: [Go minimum requirements](https://go.dev/wiki/MinimumRequirements),
[Go 1.27 release notes](https://go.dev/doc/go1.27),
[Go on FreeBSD](https://go.dev/wiki/FreeBSD). Upstream floors can change with an
SDK update and must be reviewed before changing `.go-version`.

The native integration layer relies on Git, POSIX process groups/signals, advisory
checkout locking, Unix permissions and filesystem operations. Filesystem traversal
uses Go's rooted APIs and existing no-follow checks; runtime fallbacks or explicit
errors depend on the host. No operating-system sandbox is claimed. Agent shell
commands and user validators need whatever programs the repository requires.
Modern OS/Git packaging may impose stricter requirements than Go's runtime floor.

## Three distinct kinds of evidence

- **Expected runtime compatibility:** the upstream OS/CPU ABI and project source
  review above; not proof of execution on the minimum OS version.
- **Cross-compilation:** all six distributions build without CGO. Packaging inspects
  ELF interpreter/dynamic segments and Mach-O dependencies. This establishes build
  feasibility, not native behavior.
- **Native qualification:** actual execution on an identified OS, architecture and
  Git version. Installer fixtures mock download/platform discovery to test all six
  mappings; those mappings alone are not six native environments. Opt-in native
  installer checks additionally install a real matching executable, test Git
  extension discovery and render the installed manual.

Current local environments cover macOS arm64, Linux arm64 and a FreeBSD arm64
**16-CURRENT** VM. Installer and earlier engine qualification exist on those
architectures; the current installer task does not repeat every engine scenario.
The CURRENT VM is useful for forward-compatibility diagnostics, not evidence for
released FreeBSD 14/15. Native amd64 and minimum-OS evidence remain acceptance gaps.
Workflow definitions are not evidence that a hosted runner has actually passed.

## Maintainable OS-version matrix

Keep CI small; update explicit version labels during routine toolchain reviews.
The existing native CI runs Linux amd64/arm64, macOS latest arm64 and macOS 26 Intel;
FreeBSD's amd64 VM uses **15.1-RELEASE**. Normal and race tests include deterministic
installer fixtures; each native CI job also installs a freshly built real binary
through the fixture-backed installer. No public Release availability is required.
FreeBSD arm64 has no native race detector; ordinary tests still apply.

Proposed release qualification, without adding per-version binaries:

| Cadence | Systems / purpose |
|---|---|
| Every PR | Existing current native CI matrix, including FreeBSD 15.1 amd64; no additional expensive VM jobs. |
| Before a release / SDK update | Latest maintained FreeBSD 14.x and 15.x on amd64 and arm64: same distributed binaries; install/help, Git operations, locking/signals, review/restoration and audit. Full deterministic engine tests where practical. |
| Before claiming an OS minimum | Oldest intended macOS/Linux/FreeBSD release on real native environments; distinguish runtime smoke checks from complete integration qualification. |
| As available | CURRENT FreeBSD VM for diagnostics, never the sole release gate. |

As of this review, FreeBSD lists 14.4, 14.5 and 15.1 as supported releases; use
14.5 and 15.1 for the maintained-branch release matrix, and retire obsolete minor
labels as their support ends. The previous 15.0 CI label has been advanced to 15.1.
Consult [FreeBSD's support table](https://www.freebsd.org/security/#sup) rather than
interpreting a successful build as support for every historic FreeBSD release.
No additional VM or older OS environment was created by this task.

Real-model quality, full functional parity and complete native qualification remain
open as recorded in [acceptance status](qualification.md). Installation readiness
and successful deterministic tests do not close those gates.
