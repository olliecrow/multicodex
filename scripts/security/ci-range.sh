#!/usr/bin/env bash
# Print one range shared by both CI scanners. Missing push bases scan history.
set -euo pipefail
if [[ "${GITHUB_EVENT_NAME}" == pull_request ]]; then
  printf '%s..%s\n' "$PR_BASE_SHA" "$PR_HEAD_SHA"
elif [[ -z "${PUSH_BEFORE_SHA:-}" || "$PUSH_BEFORE_SHA" == 0000000000000000000000000000000000000000 ]] || ! git cat-file -e "${PUSH_BEFORE_SHA}^{commit}" 2>/dev/null; then
  printf '%s\n' "$GITHUB_SHA"
else
  printf '%s..%s\n' "$PUSH_BEFORE_SHA" "$GITHUB_SHA"
fi
