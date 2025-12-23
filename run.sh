#!/usr/bin/env bash
set -euo pipefail

# cd to parent dir of current script
cd "$(dirname "${BASH_SOURCE[0]}")"

dbg() {
    if [[ "${DEBUG:-}" == "1" ]]; then
        echo "[DEBUG] $*" >&2
    fi
}

dbg "go version: $(go version)"
dbg "GOFLAGS=${GOFLAGS:-}"
dbg "running with -mod=mod (override readonly go.sum policy)"

exec go run -mod=mod ./cmd/i3d
