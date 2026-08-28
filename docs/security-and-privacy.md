# Security and privacy

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
- Logs and diagnostics omit secret data by default.
- User-visible diagnostics must never echo raw provider response bodies, app-server error messages, or Codex subprocess failure output. Preserve only safe status codes and allowlisted recovery guidance.
- Profile-scoped Codex subprocesses must scrub inherited Codex/OpenAI account override environment variables before setting the selected profile `CODEX_HOME`.
- Profile resource settings may name local directories outside the default Codex home. The user owns the trust decision for those sources; multicodex only creates symlinks and does not execute or copy source contents.
- Explicit resource reconciliation validates all configured sources before profile mutation. It removes or retargets only symlinks at documented managed positions and preserves regular profile guidance and skill entries.
- Editor SSH input is restricted to an alias-shaped name and is passed as one argument with batch mode.
  - Agent, X11, local, remote, dynamic, and tunnel forwarding are disabled. Configured local commands do not run.
  - SSH child processes remove inherited `CODEX_*`, `OPENAI_*`, and `MULTICODEX_*` values. Configured environment forwarding cannot transmit client account state.
  - Remote protocol errors, metadata, and pasted terminal text are bounded and stripped of terminal and bidirectional control characters before use.
- Editor Git commands run without a controlling terminal, disable interactive credential and askpass prompts, and retain configured SSH transports, agents, and credential helpers. Connection loss cancels transient host command process groups; owned tmux servers remain outside that cancellation scope.
- Editor state, locks, worktrees, and attachments use private directories below `MULTICODEX_HOME`. Short-lived SSH control sockets use an exact user-owned 0700 directory below `/tmp`. State reads and record sets are bounded; writes are atomic and directory-synced. Symlinked or permissive state paths, and symlinked, foreign-owned, or permissive SSH runtime paths, are rejected. One host-local operation lock serializes resource lifecycle changes across connections and editor instances.
- Editor terminal output is memory-only. The renderer discards terminal hyperlink metadata before screen cells can retain it and never emits child control sequences into the outer interface. Activity tracking keeps only one SHA-256 hash and timestamp per live pane for the last 200 captured rows. The client never stores remote terminal content or copies Codex auth between hosts.
- Host deletion requires deterministic paths or names and exact instance, project or workspace, and window ownership metadata.
  - Every session-targeting tmux command uses an exact session name or ID. Fuzzy matching never selects another session.
  - Editor-created Git worktrees require their recorded lock reason. Tmux, Git, filesystem, lock, or registry uncertainty stops deletion.
  - Branch changes inside an owned worktree are valid. Only its initial editor branch is eligible for deletion. Automatic cleanup never removes Git worktrees or branches.
  - Adoption accepts only a detached, ungrouped, single-pane default-server tmux session at the exact project root. Release clears exact markers but never stops the session.
  - Two-phase workspace, window, and attachment intent records make interrupted operations recoverable without expanding ownership. An interrupted worktree unlock is relocked before deletion resumes.
  - Force confirmation is never persisted or replayed after interruption.
- Attachments are bounded to 16 MiB, opened without following symlinks, copied to private host-local files, and deleted with their workspace or project terminal, or after seven days. Image input is validated as PNG or JPEG with bounded dimensions. The editor pastes a path but never submits a terminal draft.

## Repository safeguards
- `.gitignore` must ignore local auth and profile state.
- Recommended ignore coverage includes targeted current state paths: `**/multicodex/config.json` and `**/multicodex/profiles/`.
- Legacy `.multicodex/` state paths remain sensitive and should stay ignored.
- Tests must use synthetic fixtures only.
- Committed email addresses must use reserved example domains or GitHub noreply addresses. Real addresses are blocked in staged files, commit messages, pushed patches, and pull request metadata.
- Example files must never include real credentials.
- CI should run secret scanning before merge.
- Run `multicodex doctor` before release to verify leak guards.
- Committed tests, examples, logs, and review artifacts must use temporary or dummy resource paths and must not include private resource contents or machine-specific paths.
- Public editor tests must use synthetic repositories and temporary homes. Tmux integration tests use only exact random `mce-*` sockets under the system tmux temporary directory and remove them after each test. System SSH configuration, installed binaries, user projects, and all other resources outside the selected multicodex home are never test targets.

## Global auth boundary
- Multicodex must not change, restore, back up, symlink, lock, or otherwise manage the shared default Codex auth account.
- The system default Codex account is managed by normal Codex tooling outside multicodex.
- Automatic `multicodex cli`, `multicodex exec`, and `multicodex generate` routing may use the existing default Codex home as the final protected reserve only when no configured profile has usable weekly usage and the official Codex CLI confirms that the default is logged in. The bounded login-status check supports file and OS keyring credential stores, must not mutate default auth state, and must not expose default auth details or raw subprocess output.
- `multicodex generate` uses the selected home's existing managed ChatGPT authentication through Codex App Server and pins Codex's built-in OpenAI provider.
  - It ignores unrelated custom model providers and disables notification hooks.
  - It fails closed if config replaces the built-in provider or endpoint, loads a configuration lockfile, or changes the selected search mode.
  - It requests no token refresh, rejects API-key authentication, discards App Server standard error, and never reads or prints credential values.
- Generation workspaces and model catalogs are private temporary files.
  - Generation reads the bundled model catalog without an authenticated discovery request. Before account access, it verifies the effective provider, endpoint, catalog, hooks, MCP state, and search mode.
  - It uses an empty working directory, explicit file-backed client instructions or empty defaults, disabled context, an ephemeral thread, read-only sandboxing, and fail-closed handling for action requests or unexpected items.
  - Generation exposes no tools by default. Explicit `--search` exposes only native live web search, rejects inherited nested search settings such as domain or approximate-location filters, and accepts search events only during that opted-in run.
  - Native search sends prompt-derived queries and page requests through the selected subscription provider.
- Generation reads input files only from paths selected by the caller and applies a separate size limit to each file. File-read errors do not print those paths.
  - JSON result mode buffers a bounded response and exposes response text, model, effective effort, elapsed milliseconds, a native web-search item count, and numeric token usage.
  - Search items are deduplicated by nonblank lifecycle ID of at most 256 bytes. Generation fails if an item has an invalid ID, search activity has no lifecycle item, or a turn exceeds 1,024 distinct search IDs.
  - The count contains no query, URL, result, account, path, reasoning, or raw-event data. Assistant text can contain model-written citations or search-derived content.
- Monitor defaults include the global Codex home through direct read-only usage requests. Normal monitor usage may start profile-scoped read-only Codex app-server sessions only for validated multicodex profile homes. Active `CODEX_HOME`, filesystem discovery, and extra raw app-server checks require explicit monitor flags; `--include-default=false` omits the global home.
