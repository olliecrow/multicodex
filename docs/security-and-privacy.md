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
- User-visible diagnostics must never echo raw provider response bodies, app-server error messages, or Codex subprocess failure output. Preserve only safe status codes and allowlisted recovery guidance.
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

## Global auth boundary
- Multicodex must not change, restore, back up, symlink, lock, or otherwise manage the shared default Codex auth account.
- The system default Codex account is managed by normal Codex tooling outside multicodex.
- Automatic `multicodex cli`, `multicodex exec`, and `multicodex generate` routing may use the existing default Codex home as the final protected reserve only when no configured profile has usable weekly usage and the official Codex CLI confirms that the default is logged in. The bounded login-status check supports file and OS keyring credential stores, must not mutate default auth state, and must not expose default auth details or raw subprocess output.
- `multicodex generate` uses the selected home's existing managed ChatGPT authentication through Codex App Server and pins Codex's built-in OpenAI provider. It ignores unrelated custom model providers, disables notification hooks, fails closed if config replaces the built-in provider or its endpoint or loads a configuration lockfile, requests no token refresh, rejects API-key authentication, discards App Server standard error, and never reads or prints credential values.
- Generation workspaces and model catalogs are private temporary files. Generation reads the bundled model catalog without an authenticated model-discovery request, verifies its effective provider, endpoint, catalog, hooks, and MCP state before account access, and uses an empty working directory, empty client instructions, disabled context and tools, an ephemeral thread, read-only sandboxing, and fail-closed handling for action requests or unexpected items.
- Monitor defaults include the global Codex home through direct read-only usage requests. Normal monitor usage may start profile-scoped read-only Codex app-server sessions only for validated multicodex profile homes. Active `CODEX_HOME`, filesystem discovery, and extra raw app-server checks require explicit monitor flags; `--include-default=false` omits the global home.
