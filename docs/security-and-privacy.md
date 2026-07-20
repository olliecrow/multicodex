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
- Keep profile `auth.json` files readable only by the local user.
- Use atomic writes to prevent partial secret files.
- Zero secret data from logs and diagnostics by default.
- Heartbeat output must never echo raw `codex exec` stdout or stderr on failures.
- Profile-scoped Codex subprocesses must scrub inherited Codex/OpenAI account override environment variables before setting the selected profile `CODEX_HOME`.
- Profile resource settings may name local directories outside the default Codex home. The user owns the trust decision for those sources; multicodex only creates symlinks and does not execute or copy source contents.
- Explicit resource reconciliation validates all configured sources before profile mutation. It removes or retargets only symlinks at documented managed positions and preserves regular profile guidance and skill entries.

## Repository safeguards
- `.gitignore` must ignore local auth and profile state.
- Recommended ignore coverage includes targeted current state paths: `**/multicodex/config.json` and `**/multicodex/profiles/`.
- Legacy `.multicodex/` state paths remain sensitive and should stay ignored.
- Tests must use synthetic fixtures only.
- Example files must never include real credentials.
- CI should run secret scanning before merge.
- `multicodex doctor` should be used before release to verify leak-guard checks.
- Committed tests, examples, logs, and review artifacts must use temporary or dummy resource paths and must not include private resource contents or machine-specific paths.

## Public project standards inherited from broader workspace
- Default repositories to private unless explicit consent says otherwise.
- Never publish sensitive data in code, docs, issues, comments, PRs, or commit messages.
- Avoid machine-specific local paths in committed docs. Use repo-relative or dummy paths.
- Rewrite history only in rare, explicit cases.
- If going public without history, archive prior repo privately and create a fresh public repo.

## Global auth boundary
- Multicodex must not change, restore, back up, symlink, lock, or otherwise manage the shared default Codex auth account.
- The system default Codex account is managed by normal Codex tooling outside multicodex.
- `multicodex exec` may run `codex exec` with the existing default Codex home as the final protected reserve account only when no configured profile has usable weekly usage. It must not mutate default auth state or expose default auth details.
- Monitor defaults include the global Codex home through direct read-only usage requests. Normal monitor usage may start profile-scoped read-only Codex app-server sessions only for validated multicodex profile homes. Active `CODEX_HOME`, filesystem discovery, and extra raw app-server checks require explicit monitor flags; `--include-default=false` omits the global home.
