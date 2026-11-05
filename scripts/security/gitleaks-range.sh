#!/usr/bin/env bash
# usage: gitleaks-range.sh <range>
# Scan every commit in <range>, including merge diffs, with both gitleaks
# configs. Exit 1 on a leak and 2 on any error. Gitleaks can log a git error,
# scan 0 commits, and still exit 0, so its log is checked too: any ERR or FTL
# line fails, and 0 scanned commits fails when the range adds lines.
set -euo pipefail

die() {
  echo "gitleaks-range: $*" >&2
  exit 2
}

[[ "$#" -eq 1 && "$1" =~ ^[^-[:space:]][^[:space:]]*$ ]] || die "usage: gitleaks-range.sh <range>"
range="$1"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
log_opts=(--text --no-textconv --no-ext-diff -m)

commits="$(git rev-list --count "$range")" || die "invalid range"
if [[ "$commits" -eq 0 ]]; then
  echo "no commits to scan"
  exit 0
fi
added="$(git log "${log_opts[@]}" --format= --numstat "$range" | awk '$1 ~ /^[0-9]+$/ && $1 > 0 { n++ } END { print n + 0 }')" ||
  die "cannot list the changes in the range"

tmp_dir="$(mktemp -d)"
trap 'rm -r "${tmp_dir}"' EXIT
esc="$(printf '\033')"
result=0
for config in .gitleaks.toml .gitleaks-paths.toml; do
  status=0
  gitleaks git --no-banner --no-color --redact --config "${root}/${config}" \
    --log-opts="${log_opts[*]} ${range}" . > "${tmp_dir}/raw.log" 2>&1 || status=$?
  sed "s/${esc}\[[0-9;]*m//g" "${tmp_dir}/raw.log" > "${tmp_dir}/gitleaks.log"
  cat "${tmp_dir}/gitleaks.log"
  if grep -qE '(^|[[:space:]])(ERR|FTL)[[:space:]]' "${tmp_dir}/gitleaks.log"; then
    die "gitleaks logged an error (${config})"
  fi
  case "$status" in
    0) ;;
    1) result=1; continue ;;
    *) die "gitleaks exited with status ${status} (${config})" ;;
  esac
  scanned="$(sed -nE 's/.*INF ([0-9]+) commits? scanned.*/\1/p' "${tmp_dir}/gitleaks.log")"
  [[ "$scanned" =~ ^[0-9]+$ ]] || die "gitleaks reported no scanned-commit count (${config})"
  if [[ "$scanned" -eq 0 && "$added" -gt 0 ]]; then
    die "gitleaks scanned 0 commits, but the range adds lines (${config})"
  fi
done
exit "$result"
