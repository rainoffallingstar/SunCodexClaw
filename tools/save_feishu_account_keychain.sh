#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 5 ] || [ "$#" -gt 6 ]; then
  echo "Usage: $0 <account> <app_id> <app_secret> <encrypt_key> <verification_token> [bot_open_id]"
  exit 1
fi

ACCOUNT="$1"
APP_ID="$2"
APP_SECRET="$3"
ENCRYPT_KEY="$4"
VERIFICATION_TOKEN="$5"
BOT_OPEN_ID="${6:-}"
KEYCHAIN_ACCOUNT="${SUNCODEXCLAW_KEYCHAIN_ACCOUNT:-${CODEX_CLAW_KEYCHAIN_ACCOUNT:-codex-claw}}"

security add-generic-password -a "${KEYCHAIN_ACCOUNT}" -s "feishu-app-id:${ACCOUNT}" -w "${APP_ID}" -U >/dev/null
security add-generic-password -a "${KEYCHAIN_ACCOUNT}" -s "feishu-app-secret:${ACCOUNT}" -w "${APP_SECRET}" -U >/dev/null
security add-generic-password -a "${KEYCHAIN_ACCOUNT}" -s "feishu-encrypt-key:${ACCOUNT}" -w "${ENCRYPT_KEY}" -U >/dev/null
security add-generic-password -a "${KEYCHAIN_ACCOUNT}" -s "feishu-verification-token:${ACCOUNT}" -w "${VERIFICATION_TOKEN}" -U >/dev/null

if [ -n "${BOT_OPEN_ID}" ]; then
  security add-generic-password -a "${KEYCHAIN_ACCOUNT}" -s "feishu-bot-open-id:${ACCOUNT}" -w "${BOT_OPEN_ID}" -U >/dev/null
fi

echo "Saved under keychain account: ${KEYCHAIN_ACCOUNT}"
echo "Saved keychain services:"
echo "  feishu-app-id:${ACCOUNT}"
echo "  feishu-app-secret:${ACCOUNT}"
echo "  feishu-encrypt-key:${ACCOUNT}"
echo "  feishu-verification-token:${ACCOUNT}"
if [ -n "${BOT_OPEN_ID}" ]; then
  echo "  feishu-bot-open-id:${ACCOUNT}"
fi
