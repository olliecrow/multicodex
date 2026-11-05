#!/usr/bin/env bash
# pre-push hook: check every commit this push sends.
set -euo pipefail

zero_ref="0000000000000000000000000000000000000000"
from_ref="${PRE_COMMIT_FROM_REF:-}"
to_ref="${PRE_COMMIT_TO_REF:-}"
SECURITY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=reviewed-range.sh
source "${SECURITY_SCRIPT_DIR}/reviewed-range.sh"

if [[ -z "${to_ref}" || "${to_ref}" == "${zero_ref}" ]]; then
  # Deleting a remote ref sends no commits.
  exit 0
fi

if [[ -z "${from_ref}" || "${from_ref}" == "${zero_ref}" ]] || ! git cat-file -e "${from_ref}^{commit}" 2>/dev/null; then
  range="$(reviewed_fallback_range "${to_ref}")"
  revisions="$(git rev-list "$range")"
else
  revisions="$(git rev-list "${from_ref}..${to_ref}")"
fi

commits=()
while IFS= read -r commit; do
  [[ -n "${commit}" ]] && commits+=("${commit}")
done <<< "${revisions}"

if [[ "${#commits[@]}" -eq 0 ]]; then
  exit 0
fi

bash "$(dirname "${BASH_SOURCE[0]}")/check-commits.sh" push "${commits[@]}"
