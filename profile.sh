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
dbg "GO_TEST_FLAGS=${GO_TEST_FLAGS:-}"
dbg "BENCHTIME=${BENCHTIME:-}"

ts="$(date +%s)"
out_dir="${OUT_DIR:-./profiles}"
mkdir -p "$out_dir"
chmod 700 "$out_dir"
umask 077

benchtime="${BENCHTIME:-2s}"

pkgs=(
    ./internal/starlib
    ./internal/daemon
)

last_cpu=""
for pkg in "${pkgs[@]}"; do
    name="${pkg#./}"
    name="${name//\//_}"

    cpu="${out_dir}/${ts}-${name}.cpu.pprof"
    mem="${out_dir}/${ts}-${name}.mem.pprof"
    mutex="${out_dir}/${ts}-${name}.mutex.pprof"
    block="${out_dir}/${ts}-${name}.block.pprof"

    last_cpu="$cpu"

    dbg "profiling pkg=${pkg}"
    dbg "  cpu=${cpu}"
    dbg "  mem=${mem}"
    dbg "  mutex=${mutex}"
    dbg "  block=${block}"

    go test "$pkg" \
        -run '^$' \
        -bench . \
        -benchmem \
        -count 1 \
        -benchtime "$benchtime" \
        -cpuprofile "$cpu" \
        -memprofile "$mem" \
        -mutexprofile "$mutex" \
        -blockprofile "$block" \
        ${GO_TEST_FLAGS:-}
done

if [[ "${DEBUG:-}" == "1" ]]; then
    echo "[DEBUG] Profiles written to: ${out_dir}" >&2
    if [[ -n "$last_cpu" ]]; then
        echo "[DEBUG] Example: go tool pprof -http=:0 ${last_cpu}" >&2
    fi
fi
