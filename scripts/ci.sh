#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
set -eu
export GOTOOLCHAIN=local GOTELEMETRY=off
python3 - <<'CHECK'
import re,subprocess
v=tuple(map(int,re.search(r'(\d+)\.(\d+)',subprocess.check_output(['git','--version'],text=True)).groups()))
assert v >= (2,46), 'Git 2.46 or newer required'
CHECK
test -z "$(gofmt -l cmd internal)"
go mod verify
go mod tidy -diff
go vet ./...
CGO_ENABLED=0 go test -timeout 15m ./...
case "$(go env GOOS)/$(go env GOARCH)" in
 freebsd/arm64) echo 'Race detector unavailable on FreeBSD arm64' ;;
 *) CGO_ENABLED=1 go test -race -timeout 20m ./... ;;
esac
CGO_ENABLED=0 go build -trimpath -o "${TMPDIR:-/tmp}/git-ez-ci-$$" ./cmd/git-ez
binary="${TMPDIR:-/tmp}/git-ez-ci-$$"
evidence=$(mktemp -d)
trap 'rm -f "$binary"; rm -rf "$evidence"' EXIT HUP INT TERM
python3 scripts/check-interactive.py --binary "$binary" --evidence "$evidence/interactive.json"
python3 scripts/check-audit.py --binary "$binary" --evidence "$evidence/audit.json"
python3 scripts/check-signal.py --binary "$binary" --evidence "$evidence/signal.json"

python3 scripts/check-paths.py --binary "$binary" --evidence "$evidence/paths.json"
