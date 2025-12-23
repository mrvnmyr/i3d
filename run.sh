#!/usr/bin/env bash
set -euo pipefail

# cd to parent dir of current script
cd "$(dirname "${BASH_SOURCE[0]}")"

# Optional:
#   DEBUG=1 ./scripts/run.sh
#   I3D_DIR=/tmp/i3d ./scripts/run.sh
exec go run ./cmd/i3d
