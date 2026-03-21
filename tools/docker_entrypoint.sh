#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_DIR}"
export SUNCODEXCLAW_IN_CONTAINER=1

sync_codex_home() {
  local enabled="${SUNCODEXCLAW_SYNC_CODEX_HOME:-true}"
  case "${enabled}" in
    0|false|False|FALSE|no|No|NO|off|Off|OFF)
      return 0
      ;;
  esac
  if ! /app/bin/suncodexclawd codex-home sync --repo "${REPO_DIR}"; then
    echo "[warn] codex-home sync failed; continuing with env-based Codex config fallback" >&2
  fi
}

cmd="${1:-start}"
shift || true

case "${cmd}" in
  start)
    sync_codex_home
    exec /app/bin/suncodexclawd start "$@"
    ;;
  status|stop|restart|list|logs|preflight|configure|launchagents|timer|memory|sync|update)
    sync_codex_home
    exec /app/bin/suncodexclawd "${cmd}" "$@"
    ;;
  shell|bash|sh)
    exec bash
    ;;
  *)
    exec "$cmd" "$@"
    ;;
esac
