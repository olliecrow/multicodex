# Security and Privacy

## Trust model
- `multicodex` is local-only.
- No external auth relay.
- No third-party secret storage.

## Secret handling rules
- Never print auth tokens, refresh tokens, or raw credential blobs.
- Never commit auth files or secret-bearing local state.
- Never copy, sync, transmit, transfer, or share Codex auth files or auth details between machines. Each machine must sign in through the official Codex login flow.
- Keep auth directories permissioned to the local user only.
- Use atomic writes to prevent partial secret files.
- Zero secret data from logs and diagnostics by default.
- Heartbeat output must never echo raw `codex exec` stdout or stderr on failures.
- Profile-scoped Codex subprocesses must scrub inherited Codex/OpenAI account override environment variables before setting the selected profile `CODEX_HOME`.

## Repository safeguards
- `.gitignore` must ignore local auth and profile state.
- Recommended ignore coverage includes targeted current state paths: `**/multicodex/config.json` and `**/multicodex/profiles/`.
- Legacy `.multicodex/` state paths remain sensitive and should stay ignored.
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

## Global auth boundary
- Multicodex must not change, restore, back up, symlink, lock, or otherwise manage the shared default Codex auth account.
- The system default Codex account is managed by normal Codex tooling outside multicodex.
- `multicodex exec` may run `codex exec` with the existing default Codex home as a protected reserve account only when configured logged-in profiles are weekly-exhausted. It must not mutate default auth state or expose default auth details.
- Monitor defaults must stay profile-focused. Default Codex home, active `CODEX_HOME`, filesystem discovery, and app-server checks require explicit monitor flags.
