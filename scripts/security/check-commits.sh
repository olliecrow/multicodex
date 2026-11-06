#!/usr/bin/env bash
# Check outgoing commits: author and committer emails, messages, and patches.
# Each commit message is also scanned with gitleaks. A gitleaks error fails closed.
# Usage: check-commits.sh <context> <commit> [commit...]
set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "usage: check-commits.sh <context> <commit> [commit...]" >&2
  exit 2
fi
context="$1"
shift

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
gitleaks_config="${repo_root}/.gitleaks.toml"
allowed_identity_regex='^([^@]+@users[.]noreply[.]github[.]com|noreply@github[.]com|[^@]+@(example[.](com|org|net)|([a-z0-9-]+[.])*(example|invalid|test)))$'

for commit in "$@"; do
  if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
    echo "invalid commit id in ${context}: ${commit}" >&2
    exit 2
  fi
done

tmp_dir="$(mktemp -d)"
trap 'rm -r "${tmp_dir}"' EXIT

failed=0
identities="$(git log --no-walk=unsorted --format='%H %ae %ce' "$@")"
while read -r commit author committer; do
  for field in author committer; do
    email="${!field}"
    email="$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]')"
    if [[ ! "$email" =~ $allowed_identity_regex ]]; then
      echo "policy violation in ${context}: commit ${commit} has a non-noreply ${field} email" >&2
      failed=1
    fi
  done
done <<< "$identities"

git log --no-walk=unsorted --format='%H%n%s%n%b%n' "$@" > "${tmp_dir}/messages.txt"
patch_files=()
for commit in "$@"; do
  patch_file="${tmp_dir}/${commit}.diff"
  git show -m --format= --patch --no-color --text --no-textconv --no-ext-diff "$commit" > "$patch_file"
  patch_files+=("$patch_file")
done
bash "${script_dir}/check-sensitive-text.sh" --context="${context}-commit-message" "${tmp_dir}/messages.txt" || failed=1
# Default gitleaks rules catch secret types the text policy does not.
for commit in "$@"; do
  status=0
  git log -1 --format='%B' "$commit" \
    | gitleaks stdin --no-banner --no-color --verbose --redact \
      --config "$gitleaks_config" > "${tmp_dir}/gitleaks-msg.log" 2>&1 || status=$?
  if grep -qE '(^|[[:space:]])(ERR|FTL)[[:space:]]' "${tmp_dir}/gitleaks-msg.log"; then
    echo "gitleaks failed scanning commit ${commit} message" >&2
    exit 2
  fi
  case "$status" in
    0) ;;
    1)
      echo "policy violation in ${context}-commit-message: commit ${commit}" >&2
      cat "${tmp_dir}/gitleaks-msg.log" >&2
      failed=1
      ;;
    *)
      echo "gitleaks failed scanning commit ${commit} message (status ${status})" >&2
      exit 2
      ;;
  esac
done
bash "${script_dir}/check-sensitive-text.sh" --context="${context}-patch" "${patch_files[@]}" || failed=1

if [[ "$failed" -ne 0 ]]; then
  echo "Use the GitHub noreply address for commit identities." >&2
fi
exit "$failed"
