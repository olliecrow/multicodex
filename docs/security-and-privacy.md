# Security and Privacy

## Trust model
- `multicodex` is local-only.
- No external auth relay.
- No third-party secret storage.
- The editor trusts the local user account, configured SSH aliases, their normal host-key policy, and software already installed on each selected host. It opens no network listener and changes no SSH or private-network configuration.

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
- Editor SSH input is restricted to an alias-shaped name and is passed as one argument with batch mode. Remote protocol errors, metadata, and pasted terminal text are bounded and stripped of terminal control characters before use.
- Editor Git commands disable interactive credential prompts but retain configured noninteractive SSH agents and credential helpers. Connection loss cancels transient host command process groups; owned tmux servers remain outside that cancellation scope.
- Editor state, locks, sockets, worktrees, and attachments use private directories. State writes are atomic and directory-synced. Symlinked or permissive state paths are rejected.
- Editor terminal output is memory-only. Persistent activity contains only a SHA-256 hash and timestamp for the last 100 captured rows. The client never stores remote terminal content or copies Codex auth between hosts.
- Host deletion requires deterministic paths or names plus exact instance, workspace, and window ownership metadata. Tmux, Git, filesystem, or registry uncertainty stops deletion. Two-phase workspace, window, and attachment intent records make interrupted operations recoverable without expanding ownership. Force confirmation is never persisted or replayed after interruption.
- Attachments are bounded to 16 MiB, opened without following symlinks, copied to private host-local files, and deleted with their workspace or after seven days. Image input is validated as PNG or JPEG with bounded dimensions. The editor pastes a path but never submits a terminal draft.

## Repository safeguards
- `.gitignore` must ignore local auth and profile state.
- Recommended ignore coverage includes targeted current state paths: `**/multicodex/config.json` and `**/multicodex/profiles/`.
- Legacy `.multicodex/` state paths remain sensitive and should stay ignored.
- Tests must use synthetic fixtures only.
- Example files must never include real credentials.
- CI should run secret scanning before merge.
- `multicodex doctor` should be used before release to verify leak-guard checks.
- Committed tests, examples, logs, and review artifacts must use temporary or dummy resource paths and must not include private resource contents or machine-specific paths.
- Public editor tests must use synthetic repositories and temporary homes. System SSH configuration, installed binaries, user projects, and resources outside the selected multicodex home are never test targets.

## Global auth boundary
- Multicodex must not change, restore, back up, symlink, lock, or otherwise manage the shared default Codex auth account.
- The system default Codex account is managed by normal Codex tooling outside multicodex.
- Automatic `multicodex cli`, `multicodex exec`, and `multicodex generate` routing may use the existing default Codex home as the final protected reserve only when no configured profile has usable weekly usage and the official Codex CLI confirms that the default is logged in. The bounded login-status check supports file and OS keyring credential stores, must not mutate default auth state, and must not expose default auth details or raw subprocess output.
- `multicodex generate` uses the selected home's existing managed ChatGPT authentication through Codex App Server and pins Codex's built-in OpenAI provider. It ignores unrelated custom model providers, disables notification hooks, fails closed if config replaces the built-in provider or its endpoint, loads a configuration lockfile, or changes the selected search mode, requests no token refresh, rejects API-key authentication, discards App Server standard error, and never reads or prints credential values.
- Generation workspaces and model catalogs are private temporary files. Generation reads the bundled model catalog without an authenticated model-discovery request, verifies its effective provider, endpoint, catalog, hooks, MCP state, and search mode before account access, and uses an empty working directory, explicit file-backed client instructions or empty defaults, disabled context, an ephemeral thread, read-only sandboxing, and fail-closed handling for action requests or unexpected items. Generation exposes no tools by default. Explicit `--search` exposes only native live web search, rejects inherited nested search settings such as domain or approximate-location filters, and accepts search events only during that opted-in run. Native search sends prompt-derived queries and page requests through the selected subscription provider.
- Generation input files are read only from paths selected by the caller and are bounded independently. File-read errors do not print those paths. JSON result mode buffers a bounded response and exposes response text, model, effective effort, elapsed milliseconds, a numeric native web-search item count, and numeric token usage. Search items are deduplicated by nonblank lifecycle ID of at most 256 bytes. Generation fails if a search item has an invalid ID, search activity has no lifecycle item, or a turn exceeds 1,024 distinct search IDs. The count adds no query, URL, result, account, path, reasoning, or raw-event data. Assistant text can contain model-written citations or search-derived content.
- Monitor defaults include the global Codex home through direct read-only usage requests. Normal monitor usage may start profile-scoped read-only Codex app-server sessions only for validated multicodex profile homes. Active `CODEX_HOME`, filesystem discovery, and extra raw app-server checks require explicit monitor flags; `--include-default=false` omits the global home.
