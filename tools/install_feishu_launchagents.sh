#!/usr/bin/env bash
set -euo pipefail

# Deprecated wrapper: forwards to `suncodexclawd launchagents ...`.
# Prefer running:
#   ./bin/suncodexclawd launchagents <install|uninstall|status> [account|all]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

GO_DAEMON_BIN="${SUNCODEXCLAWD_BIN:-${REPO_DIR}/bin/suncodexclawd}"

usage() {
  cat <<'USAGE'
Usage:
  bash tools/install_feishu_launchagents.sh <install|uninstall|status> [account|all] [-- ...]

Notes:
  - deprecated wrapper; forwards to:
    ./bin/suncodexclawd launchagents <install|uninstall|status> [account|all]
USAGE
}

main() {
  local action="${1:-}"
  local target="${2:-all}"

  if [[ "${action}" == "" || "${action}" == "-h" || "${action}" == "--help" || "${action}" == "help" ]]; then
    usage
    exit 0
  fi

  if [[ "${action}" != "install" && "${action}" != "uninstall" && "${action}" != "status" ]]; then
    echo "[error] unknown action: ${action}" >&2
    usage >&2
    exit 2
  fi

  if [[ ! -x "${GO_DAEMON_BIN}" ]]; then
    echo "[error] missing Go daemon: ${GO_DAEMON_BIN}" >&2
    echo "hint=build it first: bash tools/build_go_bins.sh" >&2
    exit 1
  fi

  echo "note=deprecated; forwarded_to=${GO_DAEMON_BIN} launchagents ${action} ${target}" >&2

  if [[ $# -ge 2 ]]; then
    shift 2
  else
    shift 1
  fi
  exec "${GO_DAEMON_BIN}" launchagents "${action}" "${target}" "$@"
}

main "$@"
