#!/usr/bin/env bash
set -euo pipefail
checks="$(cd "$(dirname "$0")" && pwd)"
[[ ! -e "$checks/../../.github/dependabot.yml" ]]
root="$(mktemp -d)"
trap 'rm -r "$root"' EXIT
expect_status() {
  local want="$1" status=0
  shift
  "$@" > "$root/output" 2>&1 || status=$?
  if [[ "$status" != "$want" ]]; then
    echo "expected status $want, got status $status: $*" >&2
    exit 1
  fi
}
reject() { expect_status 1 "$@"; }
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

# Token families, built at runtime so no token literal is committed. Each one
# must be rejected alone and in prose, by ripgrep and by the grep fallback,
# and must not appear in the redacted output.
fallback_bin="$root/fallback-bin"
mkdir "$fallback_bin"
for tool in cat grep sed sort tr; do ln -s "$(command -v "$tool")" "$fallback_bin/$tool"; done
scan_text() {
  local mode="$1"
  shift
  if [[ "$mode" == rg ]]; then
    bash "$checks/check-sensitive-text.sh" "$@"
  else
    PATH="$fallback_bin" "$BASH" "$checks/check-sensitive-text.sh" "$@"
  fi
}
modes=(grep)
if command -v rg >/dev/null 2>&1; then
  modes+=(rg)
else
  echo "ripgrep not found; token checks use the grep fallback only"
fi
body="$(printf 'Ab3dEf6hIj9kLm2nOp5qRs8tUv1wXy4z%.0s' 1 2 3)"
jwt_header="$(printf '%s' '{"alg":"HS256","typ":"JWT"}' | base64 | tr '+/' '-_' | tr -d '=\n')"
jwt_payload="$(printf '%s' '{"sub":"fixture","iat":1700000000}' | base64 | tr '+/' '-_' | tr -d '=\n')"
tokens=(
  "sk-ant-oat01-${body}AA"
  "sk-ant-api03-${body}AA"
  "sk-proj-${body:0:74}T3BlbkFJ${body:0:74}"
  "${jwt_header}.${jwt_payload}.${body:0:43}"
  "github_pat_${body:0:22}_${body:0:59}"
)
for prefix in ghp gho ghu ghs ghr; do tokens+=("${prefix}_${body:0:36}"); done
for mode in "${modes[@]}"; do
  for token in "${tokens[@]}"; do
    for line in "$token" "note: use $token here"; do
      printf '%s\n' "$line" > "$root/input"
      reject scan_text "$mode" "$root/input"
      if grep -qF -- "$token" "$root/output"; then
        echo "token scan output is not redacted (${mode}, ${token:0:8}...)" >&2
        exit 1
      fi
    done
  done
  printf 'task-ant-colony-optimisation-notes, disk-project-management-guide, eyes.eyebrows.eyelids\n' > "$root/input"
  scan_text "$mode" "$root/input"
done
printf '%s\n' "${tokens[0]}" > "$root/input"
reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks.toml" "$root/input"

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

merge="$root/merge"
git init -q -b main "$merge"
git -C "$merge" config user.name Test
git -C "$merge" config user.email test@example.com
git -C "$merge" -c commit.gpgsign=false commit -q --allow-empty -m base
git -C "$merge" checkout -q -b side
printf 'side\n' > "$merge/side.txt"
git -C "$merge" add side.txt
git -C "$merge" -c commit.gpgsign=false commit -qm side
git -C "$merge" checkout -q main
printf 'main\n' > "$merge/main.txt"
git -C "$merge" add main.txt
git -C "$merge" -c commit.gpgsign=false commit -qm mainline
git -C "$merge" merge -q --no-commit side >/dev/null 2>&1
token="$(printf '%s-%s-%s' "xoxb" "123456789012" "1234567890123abc")"
printf 'bot=%s\n' "$token" > "$merge/secret.txt"
git -C "$merge" add secret.txt
git -C "$merge" -c commit.gpgsign=false commit -qm merge
workflow="$checks/../../.github/workflows/security-policy.yml"
# CI reaches gitleaks only through the wrapper that checks its log.
if ! grep -qF 'bash scripts/security/gitleaks-range.sh "$range"' "$workflow"; then
  echo "workflow does not scan with gitleaks-range.sh" >&2
  exit 1
fi
if grep -qE 'gitleaks (git|dir|detect)' "$workflow"; then
  echo "workflow runs gitleaks without gitleaks-range.sh" >&2
  exit 1
fi
wrapper="$checks/gitleaks-range.sh"
stub_bin="$root/stub-bin"
mkdir "$stub_bin"
printf '#!/usr/bin/env bash\ncat "$STUB_GITLEAKS_LOG"\n' > "$stub_bin/gitleaks"
chmod 0755 "$stub_bin/gitleaks"
# stub_scan <status> <log>: the wrapper must exit <status> when gitleaks
# exits 0 after printing <log> (a printf format).
stub_scan() {
  # shellcheck disable=SC2059 # the log is the format
  printf "$2" > "$root/stub.log"
  expect_status "$1" stub_wrapper
}
stub_wrapper() {
  PATH="$stub_bin:$PATH" STUB_GITLEAKS_LOG="$root/stub.log" bash "$wrapper" HEAD
}
(
  cd "$merge"
  # The secret is only in the merge diff: plain log options miss it.
  gitleaks git --no-banner --redact --config "$checks/../../.gitleaks.toml" --log-opts="--text --no-textconv --no-ext-diff HEAD"
  reject bash "$wrapper" HEAD
  expect_status 2 bash "$wrapper" 0123456789abcdef0123456789abcdef01234567..HEAD
  expect_status 2 bash "$wrapper" 'HEAD '
  expect_status 0 bash "$wrapper" HEAD..HEAD
  stub_scan 2 'ERR [git] fatal: stub failure\nINF 0 commits scanned.\nINF no leaks found\n'
  stub_scan 2 'ERR error="stderr is not empty"\nINF 2 commits scanned.\nINF no leaks found\n'
  stub_scan 2 'INF 0 commits scanned.\nINF no leaks found\n'
  stub_scan 2 'INF no leaks found\n'
  stub_scan 0 'INF 4 commits scanned.\nINF no leaks found\n'
)
repo="$root/delete"
git init -q -b main "$repo"
git -C "$repo" config user.name Test
git -C "$repo" config user.email test@example.com
printf 'clean\n' > "$repo/clean.txt"
git -C "$repo" add clean.txt
git -C "$repo" -c commit.gpgsign=false commit -qm add
git -C "$repo" rm -q clean.txt
git -C "$repo" -c commit.gpgsign=false commit -qm delete
(
  cd "$repo"
  expect_status 0 bash "$wrapper" HEAD
  # A delete-only range scans 0 commits and is still clean.
  expect_status 0 bash "$wrapper" HEAD^..HEAD
  grep -qF '0 commits scanned' "$root/output"
)
echo 'Policy regression tests passed'
