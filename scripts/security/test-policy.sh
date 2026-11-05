#!/usr/bin/env bash
set -euo pipefail
checks="$(cd "$(dirname "$0")" && pwd)"
root="$(mktemp -d)"
trap 'rm -r "$root"' EXIT
reject() {
  if "$@" > "$root/output" 2>&1; then
    echo "unexpected policy acceptance: $*" >&2
    exit 1
  fi
}
for name in YOUNG userbob; do
  printf '/%s/%s/project\n' "$( [[ "$name" == YOUNG ]] && echo Users || echo home )" "$name" > "$root/input"
  reject bash "$checks/check-sensitive-text.sh" "$root/input"
done
for prefix in '' 'note: '; do
  printf '%s/home/%s/project\n' "$prefix" fixture-person > "$root/input"
  reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
done
printf '/Users/YOU /Users/%s/project\n' fixture-person > "$root/input"
reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
printf '/Users/YOU /home/user\n' > "$root/input"
bash "$checks/check-sensitive-text.sh" "$root/input"
gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
for kind in nul attribute; do
  repo="$root/$kind"
  git init -q -b main "$repo"
  git -C "$repo" config user.name Test
  git -C "$repo" config user.email test@example.com
  printf '%s=%s\n' token abc123456789XYZ > "$repo/input"
  if [[ "$kind" == nul ]]; then printf '\0' >> "$repo/input"; else printf '* -diff\n' > "$repo/.gitattributes"; fi
  git -C "$repo" add .
  git -C "$repo" -c commit.gpgsign=false commit -qm fixture
  (
    cd "$repo"
    reject bash "$checks/check-commits.sh" test "$(git rev-parse HEAD)"
    reject gitleaks git --no-banner --redact --config "$checks/../../.gitleaks.toml" --log-opts="--text --no-textconv --no-ext-diff HEAD"
    git update-ref refs/remotes/backup/main HEAD
    printf 'clean\n' > clean
    git add clean
    git -c commit.gpgsign=false commit -qm clean
    for base in '' 0000000000000000000000000000000000000000; do
      reject env PRE_COMMIT_FROM_REF="$base" PRE_COMMIT_TO_REF=HEAD bash "$checks/check-push-range.sh"
    done
    for base in '' 0000000000000000000000000000000000000000 0123456789abcdef0123456789abcdef01234567; do
      range="$(GITHUB_EVENT_NAME=push PUSH_BEFORE_SHA="$base" GITHUB_SHA=HEAD bash "$checks/ci-range.sh")"
      [[ "$(git rev-list --count "$range")" == 2 ]]
    done
  )
done
echo 'Policy regression tests passed'
