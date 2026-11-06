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
assert_redacted() {
  if grep -qF -- "$1" "$root/output"; then
    echo 'scanner output contains the fixture value' >&2
    exit 1
  fi
}
for name in YOUNG userbob; do
  printf '/%s/%s/project\n' "$( [[ "$name" == YOUNG ]] && echo Users || echo home )" "$name" > "$root/input"
  reject bash "$checks/check-sensitive-text.sh" "$root/input"
  reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
done
# Real home paths, built at runtime, including paths the default gitleaks
# allowlist would hide and a real path next to a placeholder.
name=fixture-person
for line in "/home/${name}/project" "note: /home/${name}" "/Users/${name}/project" \
  'C:\Users\'"${name}"'\project' 'C:\\Users\\'"${name}"'\\project' "/Users/YOU /Users/${name}/project"; do
  printf '%s\n' "$line" > "$root/input"
  reject bash "$checks/check-sensitive-text.sh" "$root/input"
  reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks-paths.toml" "$root/input"
done
printf '%s\n' '/Users/YOU /home/user C:\Users\USERNAME\project' > "$root/input"
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
  "sk-${body}"
  "sk-svcacct-${body}"
  "sk-admin-${body}"
  "AKIA$(printf 'A1B2%.0s' 1 2 3 4)"
  "sk-proj-${body:0:74}T3BlbkFJ${body:0:74}"
  "${jwt_header}.${jwt_payload}.${body:0:43}"
  "github_pat_${body:0:22}_${body:0:59}"
)
for prefix in ghp gho ghu ghs ghr; do tokens+=("${prefix}_${body:0:36}"); done
for mode in "${modes[@]}"; do
  for token in "${tokens[@]}"; do
    for line in "$token" "note: use $token here" "token=$token" "\"$token\""; do
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
  # Ordinary words and identifiers must not expose a token-shaped suffix.
  for line in "ask-$(printf 'a1b2%.0s' {1..10})" "task-$(printf 'a1b2%.0s' {1..10})" \
    "task-ant-oat01-${body}" "disk-proj-${body}" "risk-assessmentreport2024final"; do
    printf '%s\n' "$line" > "$root/input"
    expect_status 0 scan_text "$mode" "$root/input"
    assert_redacted "$line"
  done
  for token in "${tokens[@]}"; do
    for prefix in x _ -; do
      printf '%s%s\n' "$prefix" "$token" > "$root/input"
      expect_status 0 scan_text "$mode" "$root/input"
      assert_redacted "$token"
    done
  done
done
printf '%s\n' "${tokens[0]}" > "$root/input"
reject gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks.toml" "$root/input"
assert_redacted "${tokens[0]}"
for line in "task-ant-oat01-${body}" "note: sk-ant-api03-${body}"; do
  printf '%s\n' "$line" > "$root/input"
  status=0
  [[ "$line" == task-* ]] || status=1
  expect_status "$status" gitleaks dir --no-banner --redact --config "$checks/../../.gitleaks.toml" "$root/input"
  assert_redacted "$body"
done

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
    # An empty, zero, or unknown push base scans all history.
    for base in '' 0000000000000000000000000000000000000000 0123456789abcdef0123456789abcdef01234567; do
      reject env PRE_COMMIT_FROM_REF="$base" PRE_COMMIT_TO_REF=HEAD bash "$checks/check-push-range.sh"
      range="$(GITHUB_EVENT_NAME=push PUSH_BEFORE_SHA="$base" GITHUB_SHA=HEAD bash "$checks/ci-range.sh")"
      [[ "$(git rev-list --count "$range")" == 2 ]]
    done
  )
done

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
  # Leak diagnostics identify the rule and source without exposing its value.
  grep -qE '^RuleID:[[:space:]]+slack-bot-token$' "$root/output"
  grep -qE '^File:[[:space:]]+secret[.]txt$' "$root/output"
  grep -qE "^Commit:[[:space:]]+$(git rev-parse HEAD)$" "$root/output"
  assert_redacted "$token"
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

# A secret type that only gitleaks knows (Slack) must not pass in a commit message.
msgrepo="$root/message-secret"
git init -q -b main "$msgrepo"
git -C "$msgrepo" config user.name Test
git -C "$msgrepo" config user.email test@example.com
printf 'clean\n' > "$msgrepo/clean.txt"
git -C "$msgrepo" add clean.txt
slack="$(printf '%s-%s-%s' "xoxb" "123456789012" "1234567890123abc")"
git -C "$msgrepo" -c commit.gpgsign=false commit -qm "$slack"
(
  cd "$msgrepo"
  reject bash "$checks/check-commits.sh" test "$(git rev-parse HEAD)"
)
grep -qF "$(git -C "$msgrepo" rev-parse HEAD)" "$root/output"
grep -qE '^RuleID:[[:space:]]+slack-bot-token$' "$root/output"
assert_redacted "$slack"
# Errors fail closed: an FTL line is not a clean scan, even when the exit code is 1.
fail_bin="$root/fail-bin"
mkdir "$fail_bin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "FTL config failed"\nexit 1\n' > "$fail_bin/gitleaks"
chmod 0755 "$fail_bin/gitleaks"
cleanrepo="$root/message-error"
git init -q -b main "$cleanrepo"
git -C "$cleanrepo" config user.name Test
git -C "$cleanrepo" config user.email test@example.com
git -C "$cleanrepo" -c commit.gpgsign=false commit -q --allow-empty -m 'clean message'
(
  cd "$cleanrepo"
  expect_status 2 env PATH="${fail_bin}:${PATH}" bash "$checks/check-commits.sh" test "$(git rev-parse HEAD)"
)
printf '#!/usr/bin/env bash\nexit 3\n' > "$fail_bin/gitleaks"
(
  cd "$cleanrepo"
  expect_status 2 env PATH="${fail_bin}:${PATH}" bash "$checks/check-commits.sh" test "$(git rev-parse HEAD)"
)

# A patch finding must name the commit, not only a combined temp file.
patchrepo="$root/patch-name"
git init -q -b main "$patchrepo"
git -C "$patchrepo" config user.name Test
git -C "$patchrepo" config user.email test@example.com
printf '%s=%s\n' token abc123456789XYZ > "$patchrepo/secret.txt"
git -C "$patchrepo" add secret.txt
git -C "$patchrepo" -c commit.gpgsign=false commit -qm 'add fixture'
patch_sha="$(git -C "$patchrepo" rev-parse HEAD)"
(
  cd "$patchrepo"
  reject bash "$checks/check-commits.sh" test "$patch_sha"
)
grep -qF "${patch_sha}.diff" "$root/output"
if grep -qF 'patches.diff' "$root/output"; then
  echo "patch finding still uses a combined temp name" >&2
  exit 1
fi
assert_redacted "abc123456789XYZ"
echo 'Policy regression tests passed'
