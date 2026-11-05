#!/usr/bin/env bash
# Print one range shared by both CI scanners.
# An empty, zero, or unknown push base uses reviewed_fallback_range.
set -euo pipefail

SECURITY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=reviewed-range.sh
source "${SECURITY_SCRIPT_DIR}/reviewed-range.sh"

if [[ "${GITHUB_EVENT_NAME}" == pull_request ]]; then
  printf '%s..%s\n' "$PR_BASE_SHA" "$PR_HEAD_SHA"
elif [[ -z "${PUSH_BEFORE_SHA:-}" || "$PUSH_BEFORE_SHA" == 0000000000000000000000000000000000000000 ]] || ! git cat-file -e "${PUSH_BEFORE_SHA}^{commit}" 2>/dev/null; then
  reviewed_fallback_range "$GITHUB_SHA"
else
  printf '%s..%s\n' "$PUSH_BEFORE_SHA" "$GITHUB_SHA"
fi
