#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
set -eu
export GOTOOLCHAIN=local GOTELEMETRY=off
case "$#:${1:-}" in
 0:|1:--native) ;;
 *) echo 'Usage: sh scripts/ci.sh [--native]' >&2; exit 2 ;;
esac
status=0
section() { printf '\n::group::%s\n' "$1"; }
end_section() { printf '::endgroup::\n'; }
run_check() {
 section "$1"; shift
 printf 'Running: %s\n' "$*"
 if "$@"; then :; else
  code=$?
  printf 'Check failed (exit %s): %s\n' "$code" "$*" >&2
  if [ "$status" -eq 0 ]; then status=$code; fi
 fi
 end_section
}
section 'Native Git minimum and toolchain'
python3 - <<'CHECK'
import re, subprocess
version = subprocess.check_output(['git', '--version'], text=True).strip()
print(version)
assert tuple(map(int, re.search(r'(\d+)\.(\d+)', version).groups())) >= (2, 46), 'Git 2.46 or newer required'
CHECK
go version
end_section
if [ "${1:-}" != --native ]; then
 run_check 'Go formatting' sh -c 'test -z "$(gofmt -l cmd internal scripts)"'
 run_check 'Module integrity' go mod verify
 run_check 'Module tidy state' go mod tidy -diff
fi
run_check 'Vet native packages' go vet ./...
run_check 'Complete native Go suite including installer fixtures' env CGO_ENABLED=0 go test -v -timeout 15m ./...
case "$(go env GOOS)/$(go env GOARCH)" in
 freebsd/arm64) echo 'Race detector unavailable on FreeBSD arm64' ;;
 *) run_check 'Complete native race suite' env CGO_ENABLED=1 go test -race -v -timeout 20m ./... ;;
esac
evidence=$(mktemp -d)
trap 'rm -rf "$evidence"' EXIT HUP INT TERM
binary="$evidence/git-ez"
run_check 'Build native installer qualification executable' env CGO_ENABLED=0 go build -trimpath -ldflags '-X main.version=v0.0.0-installer-test' -o "$binary" ./cmd/git-ez
run_check 'Interactive CLI' python3 scripts/check-interactive.py --binary "$binary" --evidence "$evidence/interactive.json"
run_check 'CLI audit' python3 scripts/check-audit.py --binary "$binary" --evidence "$evidence/audit.json"
run_check 'Signals and process cleanup' python3 scripts/check-signal.py --binary "$binary" --evidence "$evidence/signal.json"
run_check 'Repository path selection' python3 scripts/check-paths.py --binary "$binary" --evidence "$evidence/paths.json"
run_check 'Native binary installation and manual' env GIT_EZ_INSTALL_TEST_BINARY="$binary" CGO_ENABLED=0 go test -v -run '^TestInstallerNativeBinary$' ./scripts
exit "$status"
