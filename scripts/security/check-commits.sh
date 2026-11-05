#!/usr/bin/env bash
# Check outgoing commits: author and committer emails, messages, and patches.
# Usage: check-commits.sh <context> <commit> [commit...]
set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "usage: check-commits.sh <context> <commit> [commit...]" >&2
  exit 2
fi
context="$1"
shift

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
git show --format= --patch --no-color "$@" > "${tmp_dir}/patches.diff"
bash "${script_dir}/check-sensitive-text.sh" --context="${context}-commit-message" "${tmp_dir}/messages.txt" || failed=1
bash "${script_dir}/check-sensitive-text.sh" --context="${context}-patch" "${tmp_dir}/patches.diff" || failed=1

if [[ "$failed" -ne 0 ]]; then
  echo "Use the GitHub noreply address for commit identities." >&2
fi
exit "$failed"
