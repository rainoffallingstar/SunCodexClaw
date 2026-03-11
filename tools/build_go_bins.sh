#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

mkdir -p "${REPO_DIR}/bin"

export GOCACHE="${GOCACHE:-/tmp/suncodexclaw-gocache}"
export GOMODCACHE="${GOMODCACHE:-/tmp/suncodexclaw-gomodcache}"

go build -o "${REPO_DIR}/bin/suncodexclawd" ./cmd/suncodexclawd
echo "[note] suncodexclawctl merged into suncodexclawd (use: bin/suncodexclawd configure)"

echo "[ok] built:"
echo "  bin/suncodexclawd"
