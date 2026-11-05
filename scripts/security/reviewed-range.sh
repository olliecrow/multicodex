#!/usr/bin/env bash
# reviewed_fallback_range <tip> prints the range for an empty, zero, or unknown
# push base. reviewed-base holds one full SHA already accepted by review. When
# that commit is an ancestor of the tip, scanners read only later commits, so a
# removed historical fixture stays outside the fallback. Otherwise they read
# all history and fail closed.

reviewed_fallback_range() {
  local tip="$1"
  local file baseline
  file="${SECURITY_SCRIPT_DIR:?}/reviewed-base"
  if [[ ! -f "$file" ]]; then
    printf '%s\n' "$tip"
    return 0
  fi
  baseline="$(tr -d '[:space:]' < "$file")"
  if [[ "$baseline" =~ ^[0-9a-f]{40}$ ]] && git merge-base --is-ancestor "$baseline" "$tip" 2>/dev/null; then
    printf '%s..%s\n' "$baseline" "$tip"
    return 0
  fi
  printf '%s\n' "$tip"
}
