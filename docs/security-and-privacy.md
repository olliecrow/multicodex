# Security and Privacy

## Trust model
- `multicodex` is local-only.
- No external auth relay.
- No third-party secret storage.

## Secret handling rules
- Never print auth tokens, refresh tokens, or raw credential blobs.
- Never commit auth files or secret-bearing local state.
- Keep auth directories permissioned to the local user only.
- Use atomic writes to prevent partial secret files.
- Zero secret data from logs and diagnostics by default.
- Heartbeat output must never echo raw `codex exec` stdout or stderr on failures.

## Repository safeguards
- `.gitignore` must ignore local auth and profile state.
- Recommended ignore coverage includes both legacy and current state dir names: `.multicodex/` and `multicodex/`.
- Tests must use synthetic fixtures only.
- Example files must never include real credentials.
- CI should run secret scanning before merge.
- `multicodex doctor` should be used before release to verify leak-guard checks.

## Public project standards inherited from broader workspace
- Default repositories to private unless explicit consent says otherwise.
- Never publish sensitive data in code, docs, issues, comments, PRs, or commit messages.
- Avoid machine-specific local paths in committed docs. Use repo-relative or dummy paths.
- Rewrite history only in rare, explicit cases.
- If going public without history, archive prior repo privately and create a fresh public repo.

## Minimal-touch global switching principle
- Global switching should modify the minimum set of files and settings needed.
- Do not alter unrelated session or tool state.
- Provide a restore path for reverting global default safely.
