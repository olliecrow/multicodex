#!/usr/bin/env bash
set -euo pipefail
checks="$(cd "$(dirname "$0")" && pwd)"
[[ ! -e "$checks/../../.github/dependabot.yml" ]]
root="$(mktemp -d)"
trap 'rm -r "$root"' EXIT
reject() {
  local status=0
  "$@" > "$root/output" 2>&1 || status=$?
  if [[ "$status" != 1 ]]; then
    echo "expected policy rejection, got status $status: $*" >&2
    exit 1
  fi
}
for name in YOUNG userbob; do
  printf '/%s/%s/project\n' "$( [[ "$name" == YOUNG ]] && echo Users || echo home )" "$name" > "$root/input"
  reject bash "$checks/check-sensitive-text.sh" "$root/input"
  reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
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

recorded="$(tr -d '[:space:]' < "$checks/reviewed-base")"
[[ "$recorded" =~ ^[0-9a-f]{40}$ ]]
git -C "$checks/../.." merge-base --is-ancestor "$recorded" HEAD

repo="$root/baseline"
git init -q -b main "$repo"
git -C "$repo" config user.name Test
git -C "$repo" config user.email test@example.com
printf '%s=%s\n' token abc123456789XYZ > "$repo/secret.txt"
git -C "$repo" add secret.txt
git -C "$repo" -c commit.gpgsign=false commit -qm 'old fixture'
printf 'clean\n' > "$repo/clean.txt"
git -C "$repo" add clean.txt
git -C "$repo" -c commit.gpgsign=false commit -qm reviewed
reviewed="$(git -C "$repo" rev-parse HEAD)"
printf 'after\n' > "$repo/after.txt"
git -C "$repo" add after.txt
git -C "$repo" -c commit.gpgsign=false commit -qm 'after review'
scan="$root/scan"
mkdir "$scan"
cp "$checks/ci-range.sh" "$checks/check-push-range.sh" "$checks/check-commits.sh" "$checks/check-sensitive-text.sh" "$checks/reviewed-range.sh" "$scan/"
printf '%s\n' "$reviewed" > "$scan/reviewed-base"
(
  cd "$repo"
  for base in '' 0000000000000000000000000000000000000000 0123456789abcdef0123456789abcdef01234567; do
    env PRE_COMMIT_FROM_REF="$base" PRE_COMMIT_TO_REF=HEAD bash "$scan/check-push-range.sh"
    range="$(GITHUB_EVENT_NAME=push PUSH_BEFORE_SHA="$base" GITHUB_SHA=HEAD bash "$scan/ci-range.sh")"
    [[ "$range" == "$reviewed..HEAD" ]]
    [[ "$(git rev-list --count "$range")" == 1 ]]
  done
  printf '%s=%s\n' token abc123456789XYZ > later.txt
  git add later.txt
  git -c commit.gpgsign=false commit -qm 'later fixture'
  for base in '' 0000000000000000000000000000000000000000 0123456789abcdef0123456789abcdef01234567; do
    reject env PRE_COMMIT_FROM_REF="$base" PRE_COMMIT_TO_REF=HEAD bash "$scan/check-push-range.sh"
    range="$(GITHUB_EVENT_NAME=push PUSH_BEFORE_SHA="$base" GITHUB_SHA=HEAD bash "$scan/ci-range.sh")"
    # shellcheck disable=SC2046 # one commit id per word
    reject bash "$scan/check-commits.sh" test $(git rev-list "$range")
  done
)
echo 'Policy regression tests passed'
