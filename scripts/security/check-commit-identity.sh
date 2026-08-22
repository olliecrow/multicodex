#!/usr/bin/env bash
set -euo pipefail

context="commit-identity"
if [[ "${1:-}" == --context=* ]]; then
  context="${1#--context=}"
  shift
fi

if [[ "$#" -lt 1 ]]; then
  echo "usage: check-commit-identity.sh [--context=<label>] <file> [file...]" >&2
  exit 2
fi

allowlist="${COMMIT_IDENTITY_ALLOWLIST:-scripts/security/commit-identity-allowlist.txt}"
if [[ ! -f "${allowlist}" ]]; then
  echo "commit identity allow-list not found: ${allowlist}" >&2
  exit 2
fi

patterns=()
while IFS= read -r pattern || [[ -n "${pattern}" ]]; do
  [[ -z "${pattern}" || "${pattern}" == \#* ]] && continue
  patterns+=("${pattern}")
done < "${allowlist}"

if [[ "${#patterns[@]}" -eq 0 ]]; then
  echo "commit identity allow-list has no patterns: ${allowlist}" >&2
  exit 2
fi

redact_address() {
  sed -E 's#[^@[:space:]]+@[A-Za-z0-9.-]+#<redacted-email>#g'
}

shopt -s nocasematch

failed=0
for target in "$@"; do
  if [[ ! -f "${target}" ]]; then
    continue
  fi

  if [[ ! -s "${target}" ]]; then
    echo "policy violation in ${context}: ${target}: no commit identities found" >&2
    failed=1
    continue
  fi

  reported=0
  line_number=0
  while IFS= read -r address || [[ -n "${address}" ]]; do
    line_number=$((line_number + 1))

    allowed=0
    if [[ -n "${address}" ]]; then
      for pattern in "${patterns[@]}"; do
        if [[ "${address}" =~ ${pattern} ]]; then
          allowed=1
          break
        fi
      done
    fi

    if [[ "${allowed}" -eq 0 ]]; then
      if [[ "${reported}" -eq 0 ]]; then
        echo "policy violation in ${context}: ${target}" >&2
        reported=1
      fi
      if [[ -z "${address}" ]]; then
        printf '%d:<missing-email>\n' "${line_number}" >&2
      else
        printf '%d:%s\n' "${line_number}" "$(printf '%s' "${address}" | redact_address)" >&2
      fi
      failed=1
    fi
  done < "${target}"
done

shopt -u nocasematch

if [[ "${failed}" -ne 0 ]]; then
  cat >&2 <<'EOF'
Blocked by commit identity policy.
- An author or committer email is not on the allow-list, so pushing would publish it.
- Check what you are about to push with: git log --no-walk=unsorted --format='%h %ae %ce' <range>
- Fix your identity for this repository with:
    git config user.email 'ID+USERNAME@users.noreply.github.com'
  and find your exact address under GitHub Settings > Emails.
- Rewrite commits already made with the wrong identity; a push is not the place to discover it.
- If an address is genuinely legitimate for this repository, add it deliberately to
  scripts/security/commit-identity-allowlist.txt.
EOF
fi

exit "${failed}"
