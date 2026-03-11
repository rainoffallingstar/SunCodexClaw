#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_DIR}"

cmd="${1:-start}"
shift || true

case "${cmd}" in
  start)
    exec /app/bin/suncodexclawd start
    ;;
  status|stop|restart|list|logs)
    exec /app/bin/suncodexclawd "${cmd}" "$@"
    ;;
  shell|bash|sh)
    exec bash
    ;;
  *)
    exec "$cmd" "$@"
    ;;
esac
