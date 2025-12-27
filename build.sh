#!/usr/bin/env bash
set -eo pipefail

# cd to parent dir of current script
cd "$(dirname "${BASH_SOURCE[0]}")"

dbg() {
    if [[ "${DEBUG:-}" == "1" ]]; then
        echo "[DEBUG] $*" >&2
    fi
}

dbg "go version: $(go version)"
dbg "GOFLAGS=${GOFLAGS:-}"
dbg "building with -mod=mod (override readonly go.sum policy)"

mkdir -p dist
go build -mod=mod -trimpath -ldflags="-s -w" ./cmd/i3d
