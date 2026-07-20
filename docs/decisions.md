# Decision Capture Policy

This document defines how to record fixes and important decisions so future work does not re-litigate the same questions. It is written to stay accurate over time.

## When to record
- Any fix for a confirmed bug, regression, or safety issue.
- Any deliberate behavior choice that differs from intuitive defaults.
- Any trade-off decision that affects reliability, security, or user workflow.
- Any change that affects external behavior, invariants, or public interfaces.

## Where to record
Use the smallest, most local place that makes the decision obvious:
- Code comments near behavior when rationale is not obvious.
- Tests with names and assertions that encode the invariant.
- Docs when a decision is cross-cutting.

Prefer updating an existing note over creating a new file.

## What to record
- Decision
- Context
- Rationale
- Trade-offs
- Enforcement
- References

## Template
```
Decision:
Context:
Rationale:
Trade-offs:
Enforcement:
References:
```

## Current decisions for this repo
Decision: Use Go for multicodex implementation.
Context: Tool needs a secure, fast, low-dependency local CLI with strong filesystem control.
Rationale: Go provides a static binary, mature stdlib, and simple packaging for macOS and Linux.
Trade-offs: Slightly more verbose than shell scripts, but safer and easier to test. Windows is intentionally unsupported.
Enforcement: Build and test pipeline will run Go tooling only.
References: `docs/requirements.md`, `docs/security-and-privacy.md`

Decision: Keep account use profile-local and never switch the shared default Codex account.
Context: The default Codex account is normal system Codex state and must stay outside multicodex ownership.
Rationale: Binding non-default accounts to profile-local `CODEX_HOME` values reduces the chance that one account workflow changes another account's auth, sessions, threads, `/goal`, or remote-control state.
Trade-offs: Users must launch profile-scoped commands explicitly instead of changing a global account.
Enforcement: `multicodex cli`, `multicodex exec`, and `multicodex heartbeat` route Codex subprocesses through profile-local `CODEX_HOME` values and scrub inherited account-routing environment. No command changes, restores, or manages the shared default Codex auth account.
References: `docs/requirements.md`, `docs/command-spec.md`

Decision: Never handle raw secrets directly in multicodex internals unless unavoidable.
Context: Strong privacy and confidentiality requirements.
Rationale: Lower blast radius and simplify trust model for open-source readiness.
Trade-offs: Some actions delegate to official `codex login` flows.
Enforcement: No secret logging, strict file permissions, secret-safe tests and fixtures.
References: `docs/security-and-privacy.md`

Decision: Do not move Codex auth between machines.
Context: Multicodex stores profile auth as local machine state, and copied auth can leak account access or break token refresh flows.
Rationale: Fresh local login is safer and matches the official Codex auth model.
Trade-offs: New machines must sign in to each profile again.
Enforcement: Docs forbid copying, syncing, transmitting, transferring, or sharing Codex auth files or auth details between machines; setup guidance should always use official `codex login` or `multicodex login`.
References: `docs/security-and-privacy.md`

Decision: Ship explicit `doctor` and `dry-run` helper commands.
Context: Similar user-facing repos use non-mutating preflight and preview helpers to reduce setup confusion and avoid accidental side effects.
Rationale: Users can validate environment and understand exactly what commands would do before running mutating operations.
Trade-offs: More command surface area, but lower operational risk and better onboarding.
Enforcement: `doctor` and `dry-run` implementations are non-mutating and covered by tests.
References: `internal/multicodex/doctor.go`, `internal/multicodex/dry_run.go`, `README.md`, `docs/command-spec.md`

Decision: Allow account-like profile names with `@`.
Context: Users may naturally use account identifiers such as email-like names for profiles.
Rationale: Better usability with minimal additional risk because path-unsafe separators remain disallowed and stored profile keys are checked again when config is loaded.
Trade-offs: Slightly broader allowed character set.
Enforcement: Validation allows account-like names but rejects empty names, path separators, unsupported punctuation, and dot-only names such as `.` and `..`; config loading rejects invalid stored profile keys before commands use them.
References: `internal/multicodex/validate.go`, `internal/multicodex/validate_test.go`, `internal/multicodex/config.go`, `internal/multicodex/config_test.go`

Decision: `status` should extract account email from local profile auth when CLI output does not include it.
Context: `codex login status` does not always print account email, which made status output less useful.
Rationale: Reading email claim from local `id_token` gives consistent user-facing account identification.
Trade-offs: Additional local parsing logic for JWT payload.
Enforcement: Status helper and unit tests.
References: `internal/multicodex/status.go`, `internal/multicodex/status_test.go`

Decision: Add doctor leak guards and auth-permission normalization.
Context: Users need confidence that auth details are handled safely and do not get committed.
Rationale: Proactive checks for repo leakage plus enforced `0600` auth-file permissions reduce accidental disclosure risk.
Trade-offs: Slightly more checks and warnings in doctor and status output. The multicodex state home, profile directories, profile `auth.json`, profile `codex-home`, and profile skills directory must be regular profile-local filesystem entries with local-user-only directory permissions; `config.toml` may still be a symlink to shared default config.
Enforcement: Doctor checks for path isolation, ignore coverage including legacy `.multicodex/` state, tracked sensitive files, auth-file symlinks, auth-file hard links, and symlinked profile homes. Setup, status, monitor profile discovery, and profile-scoped Codex execution preflights reject unsafe profile paths before running Codex; setup repairs existing state directories to `0700`; config saves use exclusive temp files and config mutations use a local config lock; profile skill repair rejects symlinked skill directories; selected-profile metadata is confined to `MULTICODEX_HOME/run`; selected-profile metadata, heartbeat lock files, and config lock files reject symlinks and hard links before use; heartbeat also rejects symlinked non-platform lock ancestors; normal auth reads reject profile `auth.json` files with group or world permissions, while login normalizes regular auth files to `0600`; tests cover helper logic.
References: `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`, `internal/multicodex/security.go`, `internal/multicodex/security_test.go`

Decision: Normalize configured paths and path comparisons before using them.
Context: On macOS, equivalent paths may appear as `/var/...` and `/private/var/...`, causing false negatives in subpath checks.
Rationale: Expanding `~`, converting relative configured homes to absolute paths, and canonicalizing through existing parent symlinks avoids bypasses and keeps core commands, monitor code, and leak guards from disagreeing about the same location.
Trade-offs: Slightly more path-resolution logic.
Enforcement: Core path resolution normalizes `MULTICODEX_HOME` and `MULTICODEX_DEFAULT_CODEX_HOME`; canonical path helpers cover leak guards; tests cover `~`, relative paths, and symlink aliases.
References: `internal/multicodex/paths.go`, `internal/multicodex/paths_test.go`, `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`

Decision: Bound profile status latency with per-call timeout and parallel profile checks.
Context: Battle tests showed `status` and `doctor` could become slow with multiple profiles or hanging `codex login status` calls.
Rationale: Timeout plus bounded parallelism keeps CLI responsive while preserving deterministic output ordering.
Trade-offs: More concurrent subprocesses and slightly more code complexity.
Enforcement: Timeout handling in status logic, worker-limited parallel collection, and timeout regression tests.
References: `internal/multicodex/status.go`, `internal/multicodex/status_timeout_test.go`, `internal/multicodex/doctor.go`

Decision: Add a profile-scoped `heartbeat` keepalive command using minimal Codex calls.
Context: Users want a simple fire-and-forget way to keep subscription windows active and verify each logged-in profile still works.
Rationale: Running `codex exec --skip-git-repo-check --ephemeral --sandbox read-only --color never hello` inside each profile context is simple, independent, compatible with official auth flows, and avoids polluting session history.
Trade-offs: Heartbeat sends a tiny real request for each logged-in profile, so there is a small per-run usage cost.
Enforcement: `multicodex heartbeat` first checks login state per profile, skips logged-out profiles, and exits non-zero for failures or no logged-in profiles.
References: `internal/multicodex/heartbeat.go`, `internal/multicodex/heartbeat_test.go`, `README.md`, `docs/command-spec.md`

Decision: Redact heartbeat failure details from raw CLI output.
Context: Raw `codex exec` failure text may include sensitive strings and should not be reflected in multicodex output.
Rationale: Returning deterministic, non-sensitive status text lowers leakage risk while preserving actionable diagnostics.
Trade-offs: Less verbose error context in the CLI output.
Enforcement: Heartbeat failure details use generic messages with timeout or exit code metadata only; known safe diagnostics (for example missing `codex` binary) are explicit; table output truncates long profile names to keep terminal output readable; tests assert secret-like output is not surfaced.
References: `internal/multicodex/heartbeat.go`, `internal/multicodex/heartbeat_test.go`, `docs/security-and-privacy.md`

Decision: Add built-in command help topics and shell completion generation.
Context: Users need fast command discovery and low-friction tab completion for daily usage.
Rationale: `help [command]` and `completion <shell>` reduce onboarding friction and repeated lookup time while keeping behavior local and deterministic.
Trade-offs: Slightly larger command surface area.
Enforcement: Help topics are maintained in one table; completion scripts include dynamic profile-name completion via local `__complete-profiles`.
References: `internal/multicodex/help.go`, `internal/multicodex/completion.go`, `internal/multicodex/help_completion_test.go`, `README.md`

Decision: Default persistent multicodex state to `~/multicodex`.
Context: Users may run multiple checkouts and worktrees; one stable home-level state directory reduces fragmentation and accidental repo-local storage.
Rationale: A single predictable directory improves safety and operational consistency without moving unrelated local state.
Trade-offs: Users who want a different state location must set `MULTICODEX_HOME` explicitly.
Enforcement: `ResolvePaths` defaults to `~/multicodex` and tests cover defaulting, explicit override behavior, and non-mutation of hidden local state.
References: `internal/multicodex/paths.go`, `internal/multicodex/paths_test.go`, `README.md`, `docs/implementation-notes.md`

Decision: Use Go `cmd/` and `internal/` layout for public-facing maintainability while preserving behavior.
Context: The initial implementation was flat in the repo root and had become harder to scan as command surface and checks expanded.
Rationale: `cmd/multicodex` for entrypoint and `internal/multicodex` for implementation aligns with common Go conventions and improves contributor onboarding without changing user-visible behavior.
Trade-offs: File moves add short-term churn in docs and references.
Enforcement: Entrypoint lives in `cmd/multicodex/main.go`; implementation and tests live in `internal/multicodex`; battletest plus unit/race/vet checks validate parity after refactor.
References: `cmd/multicodex/main.go`, `internal/multicodex/`, `README.md`, `docs/implementation-notes.md`

Decision: Prefer targeted multicodex state ignore patterns over broad `multicodex/` path ignores.
Context: After introducing `internal/multicodex`, a broad `multicodex/` ignore rule risked masking source directories and weakening review safety.
Rationale: Explicit patterns for `config.json` and `profiles/` retain secret-safety goals without accidentally hiding tracked source files.
Trade-offs: Slightly longer ignore patterns and doctor guidance.
Enforcement: `.gitignore` uses targeted patterns; doctor missing-pattern checks require current `multicodex` state patterns; tests assert coverage.
References: `.gitignore`, `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`, `docs/security-and-privacy.md`

Decision: Verify newly added profiles without adding a generic profile-scoped command runner.
Context: `codex login status` proves auth is present, but the narrowed command surface no longer includes a generic `run` command.
Rationale: Keeping verification inside existing supported commands avoids reintroducing broad process-launch capability while still checking profile wiring.
Trade-offs: A profile-specific live request is now done through `multicodex cli <name>` instead of a one-shot generic wrapper; `multicodex exec` remains auto-routed across configured profiles.
Enforcement: Manual verification uses `multicodex status`, an optional `multicodex cli <name>` read-only prompt, and a follow-up `multicodex status` check. Automated exec routing is covered through `multicodex exec` tests.
References: `README.md`, `docs/implementation-notes.md`, `docs/command-spec.md`

Decision: Make heartbeat cron-safe with local locking, bounded retries, and read-only execution.
Context: Scheduled keepalive runs should not overlap, should tolerate transient failures, and should never need to mutate the current workspace or the default Codex account.
Rationale: A local OS lock avoids duplicate overlapping work, one retry with linear backoff handles short-lived provider hiccups, and forcing `codex exec` into ephemeral read-only mode avoids session-history pollution and reduces accidental side effects during automated refresh runs.
Trade-offs: Slightly more heartbeat code and a small delay before final failure when retries are used.
Enforcement: `multicodex heartbeat` acquires a non-blocking lock under multicodex home, retries failed profile heartbeats, runs `codex exec` with `--ephemeral --sandbox read-only`, and keeps all auth routing profile-scoped via `CODEX_HOME`. Exact-argv tests prevent persistent execution from returning accidentally.
References: `internal/multicodex/heartbeat.go`, `internal/multicodex/heartbeat_test.go`, `README.md`, `docs/command-spec.md`

Decision: Fold subscription usage monitoring into multicodex under a namespaced `monitor` command.
Context: Users choose between multiple Codex accounts based on both account isolation and remaining subscription headroom, so keeping switching and monitoring in separate products created an avoidable split workflow.
Rationale: One product with a dedicated `monitor` namespace matches the real user workflow while keeping usage visibility clearly separated from mutating account-management commands.
Trade-offs: The repo and CLI gain more code and dependencies, so the monitor must stay modular and avoid bloating the root command surface.
Enforcement: The integrated monitor lives under `internal/monitor/`; the primary user entrypoint is `multicodex monitor`; default monitor account candidates come only from monitor-owned account overrides and configured multicodex profiles. The default Codex home, the active `CODEX_HOME`, filesystem discovery, and extra raw app-server diagnostics are explicit opt-ins. When the same Codex home appears more than once, labels and source details prefer monitor-owned overrides, then multicodex profiles, then any opt-in sources by their configured priority.
References: `internal/multicodex/monitor.go`, `internal/monitor/usage/accounts.go`, `internal/monitor/tui/model.go`, `README.md`, `docs/command-spec.md`, `docs/implementation-notes.md`

Decision: Keep monitor helper subcommands under the `monitor` namespace.
Context: Users need monitor-specific help, terminal UI launch, setup checks, and shell completion from the same namespaced command group.
Rationale: Keeping these helpers under `multicodex monitor` makes the monitor workflow discoverable without adding more top-level commands.
Trade-offs: Slightly more command-surface and completion/help maintenance.
Enforcement: `multicodex monitor completion [shell]` remains available with bash default; explicit UI command `multicodex monitor tui` remains help-addressable; help topics and shell completion include nested monitor topics such as `monitor doctor`, `monitor completion`, and `monitor tui`.
References: `internal/multicodex/monitor.go`, `internal/multicodex/help.go`, `internal/multicodex/completion.go`, `internal/multicodex/monitor_test.go`, `README.md`, `docs/command-spec.md`

Decision: Default profile config to the shared global Codex config, while preserving explicit per-profile overrides.
Context: Users expect Codex feature settings such as search or model defaults to stay consistent across regular Codex usage and multicodex profile usage without copying config files into each profile.
Rationale: A profile-local symlink to the default Codex `config.toml` keeps settings current automatically as the global config changes, while leaving any non-generated profile-local config file intact preserves an escape hatch for account-specific customization.
Trade-offs: Auth isolation now depends more directly on the default Codex config using file-backed credentials; profile login must fail clearly when the effective config would not use file-backed auth; doctor output must explain shared-config states clearly.
Enforcement: New profiles create a `config.toml` symlink to the default Codex config; generated profile configs stay aligned with that symlink policy; manually maintained profile config files are preserved as overrides; `multicodex login`, `multicodex status`, and profile-scoped Codex execution paths reject configs that do not enable file-backed auth before exporting profile env or running Codex.
References: `internal/multicodex/config.go`, `internal/multicodex/config_test.go`, `internal/multicodex/doctor.go`, `README.md`, `docs/implementation-notes.md`

Decision: Present monitor identities and timestamps for operator readability while keeping internal timekeeping canonical.
Context: The monitor aggregates account usage across multiple Codex homes, but raw email addresses and UTC timestamps with seconds make the TUI harder to scan during live account selection.
Rationale: Showing configured account labels instead of emails keeps the UI aligned with the names users chose in multicodex, and rendering user-facing timestamps in local time at minute precision improves readability without changing internal UTC tracking.
Trade-offs: Duplicate or missing account labels can still make the display ambiguous, so the UI falls back to stable account or user IDs when labels are unavailable; local rendering means screenshots differ across operator time zones.
Enforcement: `internal/monitor/tui/model.go` renders window titles and account summaries from labels first, keeps active-account matching keyed off stable identity fields, and formats user-facing timestamps in local time without seconds; `internal/monitor/tui/model_test.go` asserts label-first titles and local-time header/reset formatting.
References: `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Sort monitor TUI account cards by weekly reset.
Context: The monitor can show several subscription accounts at once, and the user wants the card order to answer which weekly subscription window resets first.
Rationale: Weekly reset order matches the main account-selection question better than active-account-first order. The order updates on each successful monitor refresh because the TUI sorts from the latest fetched window data.
Trade-offs: The active account no longer stays pinned at the top in multi-account mode, and accounts without a known weekly reset appear last.
Enforcement: `internal/monitor/tui/model.go` sorts multi-account rows by the weekly window reset, with unknown weekly reset times last and account name as the tie-breaker. `internal/monitor/tui/model_test.go` asserts weekly ordering, short-viewport retention of the earliest weekly reset row, and unknown-weekly-reset-last behavior.
References: `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Add `multicodex exec` as an auto-routing wrapper around `codex exec`.
Context: Users often want the convenience of `codex exec` without manually choosing which logged-in subscription account currently has the most weekly headroom.
Rationale: A dedicated `multicodex exec` command preserves a simple, familiar interface while keeping account-selection policy explicit and local to multicodex.
Trade-offs: Selection is snapshot-based, so simultaneous launches can still choose the same account and the chosen account may change between invocations. The protected default account can also be used as the final fallback even when its usage data is unavailable or exhausted; that accepts possible Codex-side failure to satisfy the rule that a prompt should be sent somewhere when any destination exists.
Enforcement: `multicodex exec` forwards all arguments directly to `codex exec`, bypasses profile selection only for exact help requests, and excludes configured profiles at 100% weekly usage. Exec orders configured profiles by selection priority, then known weekly reset soonest, then unknown weekly reset, with randomness only for exact ties. The default Codex home is a protected reserve and is used only when no configured profile has usable weekly usage, including when there are no configured profiles. If the default Codex home is the only remaining destination, exec uses it as the final fallback even when its weekly usage is unavailable or exhausted. Unsafe configured profile paths still fail before routing instead of being hidden by global fallback. Tests assert weekly exhaustion and missing-data handling, reset ordering, exact ties, protected reserve execution, no-profile reserve execution, and final reserve fallback.
References: `internal/multicodex/exec.go`, `internal/multicodex/exec_test.go`, `internal/monitor/usage/select.go`, `internal/monitor/usage/select_test.go`, `README.md`, `docs/command-spec.md`

Decision: Parse `cli_auth_credentials_store` by exact key instead of substring matching.
Context: Shared profile configs rely on the default Codex `config.toml`, so auth-isolation checks must inspect the real credential-store setting rather than unrelated comments or strings.
Rationale: A small exact-key parser removes false positives without adding a TOML dependency and keeps login, doctor, exec, heartbeat, and profile-scoped Codex checks aligned.
Trade-offs: Slightly more parsing code to maintain, but much lower risk of silently misclassifying auth isolation.
Enforcement: All file-store checks route through the shared parser and regression tests cover comments, unrelated strings, and nested tables.
References: `internal/multicodex/config.go`, `internal/multicodex/config_test.go`, `internal/multicodex/doctor.go`, `internal/multicodex/app.go`

Decision: Keep read-only command discovery from changing local state.
Context: Help, completion, status, doctor, monitor, dry-run, and unknown commands are often used for inspection or shell setup. Running them should not move local multicodex state.
Rationale: Read-only commands need to be safe probes, especially while the default Codex account is outside multicodex ownership.
Trade-offs: Mutating setup happens only in commands that explicitly create or update multicodex-owned state.
Enforcement: `RunCLI` handles top-level help, direct command help such as `cli --help`, version, and `exec --help` before path resolution, validates unknown commands before path resolution, and uses read-only path resolution for read-only commands. `status` loads existing config without creating a fresh home, and monitor commands no longer create the monitor data dir just to inspect usage. Tests cover help, unknown commands, status, command help, and `exec --help` leaving local state untouched.
References: `cmd/multicodex/main.go`, `internal/multicodex/app.go`, `internal/multicodex/monitor.go`, `internal/multicodex/run_cli_test.go`, `internal/multicodex/paths.go`, `internal/multicodex/paths_test.go`

Decision: Clear stale profile and account environment for Codex subprocesses.
Context: Commands can be launched from a shell that still has profile-scoped `CODEX_HOME`, multicodex metadata, or account-token environment variables.
Rationale: A profile-scoped command should use only the intended profile home, and neutral help paths should behave like direct Codex help. Inherited account overrides could silently bypass profile auth isolation.
Trade-offs: Callers that want profile-specific behavior must use a profile-scoped command instead of relying on inherited environment.
Enforcement: Shared environment helpers strip stale `CODEX_HOME`, multicodex profile metadata, heartbeat lock or obsolete heartbeat environment, and OpenAI/Codex account override variables before adding the intended `CODEX_HOME`; `exec --help`, top-level doctor version probes, monitor doctor version probes, and opt-in monitor app-server startup strip the same sensitive environment classes; tests cover each case.
References: `internal/multicodex/process.go`, `internal/multicodex/exec.go`, `internal/multicodex/exec_help_test.go`, `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`, `internal/monitor/usage/accounts.go`, `internal/monitor/usage/accounts_test.go`, `internal/monitor/usage/appserver.go`, `internal/monitor/usage/appserver_test.go`, `internal/monitor/usage/doctor.go`, `internal/monitor/usage/doctor_test.go`

Decision: Use Codex app-server first for validated multicodex profile homes, with direct OAuth as fallback.
Context: Direct OAuth usage fetches fail when a profile's access token has expired, even if the profile is still logged in and Codex app-server can read usage through the normal Codex auth path.
Rationale: Validated multicodex profile homes are already checked for profile-local file-backed auth, so the monitor can safely match Codex CLI auth handling for those profiles instead of treating refreshable credentials as logged out. Unvalidated monitor account homes stay on direct OAuth so app-server does not follow a different credential store than the local `auth.json` path.
Trade-offs: Normal monitor refreshes may start profile-scoped read-only Codex app-server sessions for validated profile homes. This is heavier than direct OAuth, but it avoids false `auth expired` rows for profiles that Codex itself can still use while preserving stricter boundaries for monitor account overrides and discovered homes.
Enforcement: Account discovery marks validated multicodex profile homes as app-server-safe; account fetchers use app-server first only for those homes and use direct OAuth for other homes unless they dedupe with a validated profile home. `monitor doctor` checks the same normal usage path by default and only adds separate raw app-server checks when `--app-server` is passed. Exit status succeeds when at least one usage fetch works, with degraded status when another fetch or setup check fails.
References: `internal/monitor/usage/accounts.go`, `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/source.go`, `internal/monitor/usage/doctor.go`, `internal/monitor/usage/accounts_test.go`, `internal/monitor/usage/fetcher_test.go`, `README.md`, `docs/command-spec.md`

Decision: Keep last good official monitor window cards visible during full refresh outages, and prioritize concrete fetch failures in diagnostics.
Context: The monitor can hit short periods where every official usage fetch fails together even though the last good official data is still useful and the local token estimate still refreshes. In that state, blanking every window card to `unavailable` is noisy and hides the more useful fetch error.
Rationale: Showing stale-but-real official window cards is better than dropping all cards at once during a transient outage. When there is also a real fetch error, operators need that concrete error more than a generic `window cards are unavailable` summary.
Trade-offs: The window cards can now stay on screen briefly after a failed refresh, so the UI must mark them stale clearly to avoid implying they are fresh.
Enforcement: When a refresh returns zero successful official account fetches, the TUI keeps the last good official window snapshot on screen, marks every official window panel as stale, and keeps the newest observed-token estimate and warnings. The diagnostics summary now prefers auth-expired warnings first, then active-account fetch failures, then other fetch failures, before generic active-window availability warnings.
References: `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Default monitor polls use a 60-second fetch timeout.
Context: With multiple accounts, the live monitor can still miss healthy official window data when the whole refresh shares one fetch budget and cold or busy account fetches run longer than 20 seconds.
Rationale: Matching the default fetch timeout to the existing 60-second poll clock gives one full refresh cycle for the current fetch pipeline and reduces false `unavailable` windows in larger real setups. The fetcher also caps the time reserved for fallback so a long refresh budget is not still split 50/50 between primary and fallback attempts.
Trade-offs: A truly degraded refresh now takes longer to surface as failed by default, but operators can still lower it with `--timeout` when they want faster failure.
Enforcement: The TUI default timeout and the user-facing monitor/help usage strings default to 60 seconds; shorter timeouts remain available via `--timeout` for operators who prefer faster failure. When fallback is available, the fetcher caps reserved fallback time so the primary path keeps most of a long refresh budget.
References: `internal/monitor/tui/model.go`, `internal/multicodex/monitor.go`, `internal/multicodex/help.go`, `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`

Decision: Active account fetches bypass the shared inactive-account worker pool.
Context: The monitor fetcher limits background account work to four pooled workers, which can otherwise leave the active account queued behind slower inactive accounts in larger setups.
Rationale: Starting active-account fetches immediately preserves the official window cards even when inactive accounts are still timing out or backing up the shared pool.
Trade-offs: A refresh can now run one or two extra concurrent active fetches outside the inactive-account pool, which slightly increases peak concurrency in exchange for more reliable active-window availability.
Enforcement: Accounts whose homes match the active home or its resolved auth-symlink alias start outside the pooled semaphore; regression tests cover the saturated-worker case.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`

Decision: Observed token estimates add local session usage across same-account homes.
Context: The monitor's observed token totals come from per-home session logs, and the same account can be used through more than one Codex home such as `~/.codex` and a multicodex profile home.
Rationale: Taking the maximum observed total for one account identity drops real local usage from the smaller home, while summing the per-home estimates matches what the monitor is actually measuring: local session-log activity across the discovered homes.
Trade-offs: If someone manually duplicates the same session logs into more than one home, the observed estimate can overcount, but normal multicodex homes keep separate session stores and the old maximum rule could undercount normal real usage.
Enforcement: Summary-level observed token estimates add same-identity home totals instead of taking the maximum; the TUI shows the weekly total as a token estimate and shows `partial` directly when some home estimates are missing.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Prefer a plain-English re-login warning when monitor fetches fail because a profile token expired.
Context: The monitor can detect expired profile auth from both the app-server path and the oauth fallback, but the raw provider error is long and easy to miss in the TUI diagnostics line.
Rationale: A short warning such as `account "work" auth expired; sign in again` tells the operator what to do next without hiding the underlying failure from deeper debug output.
Trade-offs: The top-level warning is less literal than the raw provider response, so the account row still keeps the original error text for debugging and tests.
Enforcement: Multi-account monitor summaries collapse token-expired fetch errors into plain-English account warnings, and the TUI diagnostics priority prefers those re-login warnings ahead of generic account fetch failures when no active-window warning is present.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Declared weekly-only provider responses stay visible and usable.
Context: Some accounts return only one official usage window, and providers can place a weekly window in the raw primary response field while omitting the raw secondary field. Treating that response as a full fetch failure makes the account look broken and can wrongly bypass configured profiles during automatic routing.
Rationale: Operators need to see the weekly data that exists, regardless of its raw transport position.
Trade-offs: Older responses without declared durations still require a narrow positional fallback. An undeclared raw secondary window is treated as weekly; an undeclared primary-only response stays unknown rather than being guessed.
Enforcement: Usage normalization extracts declared 10,080-minute windows regardless of raw response position and otherwise uses only the older secondary-position fallback. The normalized model, TUI, and routing expose a weekly window only.
References: `internal/monitor/usage/raw_types.go`, `internal/monitor/usage/raw_types_test.go`, `internal/monitor/usage/select.go`, `internal/monitor/usage/select_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Use weekly usage as the only normalized account limit.
Context: The provider's useful current account signal is the weekly default or Spark bucket. Keeping an obsolete shorter window in routing, estimates, metadata, and the monitor adds noise and can choose a worse account.
Rationale: One explicit weekly model makes eligibility, reset ordering, local estimates, metadata, tests, and the TUI agree on the same account-selection contract.
Trade-offs: Selected-profile metadata now emits only `weekly_used_percent`; consumers of the two older optional percent names must update. Raw primary/secondary fields remain only while decoding provider payloads and supporting the narrow older positional fallback.
Enforcement: Normalized summaries and model buckets expose one weekly window. Routing orders by selection priority, known weekly reset soonest, then unknown reset, and randomizes only exact ties. Local session estimates use one seven-day cutoff. The TUI renders one full-width weekly card per account with default and Spark lines. Selected-profile metadata emits only `weekly_used_percent`.
References: `internal/monitor/usage/model.go`, `internal/monitor/usage/raw_types.go`, `internal/monitor/usage/select.go`, `internal/monitor/usage/observed_tokens.go`, `internal/multicodex/exec.go`, `internal/monitor/tui/model.go`, `README.md`, `docs/command-spec.md`

Decision: Default-branch-first day-to-day workflow is acceptable in this personal repo.
Context: This repository is part of the user's personal GitHub portfolio and often supports experimental or fast-iteration work. The user explicitly prefers to work directly on the default branch for normal day-to-day changes unless there is a task-specific reason to branch.
Rationale: Working directly on the default branch keeps personal-repo execution simple and fast. Branches remain available when they materially help with coordination, isolation, or review.
Trade-offs: There is less branch isolation by default, so targeted staging, small checkpoints, and verification still matter.
Enforcement: Agents may use the repository's default branch for normal personal-repo work unless the user requests a separate branch or the task clearly benefits from one.
References: `AGENTS.md`, `docs/workflows.md`, `README.md`

Decision: `multicodex cli <profile>` uses shared Codex config defaults instead of injecting multicodex defaults.
Context: The user's default model, reasoning level, permissions, and other Codex config should stay shared across normal Codex and multicodex profile sessions.
Rationale: Letting Codex read the shared `config.toml` keeps config behavior simple and avoids multicodex becoming another source of model or permission defaults.
Trade-offs: Users who want command-specific overrides must pass normal Codex CLI args explicitly.
Enforcement: `multicodex cli <profile> [codex args...]` launches `codex` in the selected profile context and forwards only user-supplied args. In real interactive terminals it hands off directly into `codex`. Tests cover args, profile env, the interactive handoff path, help, concurrent profile-local state, and auth-isolation preflight.
References: `internal/multicodex/cli.go`, `internal/multicodex/cli_test.go`, `README.md`, `docs/command-spec.md`

Decision: Keep profile resource sharing optional and preserve the established behavior when it is omitted.
Context: Profiles already inherit missing top-level skills from the default Codex skills tree, while profile guidance is unmanaged. Some users also need shared guidance or more than one skill source without a second config system or copied trees.
Rationale: One optional `profile_resources` block adds explicit guidance and ordered skill sources while keeping every old config and omitted setting on the original path. Required booleans and strict nested decoding prevent a typo from becoming destructive isolation.
Trade-offs: Explicit management owns symlinks at the documented profile positions, so a pre-existing custom symlink can be retargeted or removed. Regular files and directories remain overrides, and every removal or retarget reports the old target.
Enforcement: Config loading checks resource shape and contradictions without filesystem access. A shared read-only resolver expands `~`, resolves relative paths from `config.json`, and validates sources for doctor, dry-run, and mutation paths. Reconciliation validates the full desired set first, treats either regular guidance file as a whole-pair override, merges skill sources in order, preserves regular overrides, and keeps the nil policy on the original guidance no-op and strict default-skill code.
References: `internal/multicodex/resources.go`, `internal/multicodex/resources_test.go`, `internal/multicodex/config.go`, `README.md`, `docs/command-spec.md`, `docs/implementation-notes.md`, `docs/security-and-privacy.md`

Decision: Route `multicodex exec` model-aware to Spark buckets when the model name requests Spark.
Context: A subscription snapshot can include both default (`codex`) and Spark (`codex_bengalfox`/Spark-name) weekly buckets, and Spark model names should use Spark quota.
Rationale: Using Spark weekly usage for Spark model names gives better quota fit for model-appropriate routing, while still preserving the rule that the default Codex home is the final destination when no configured profile can take the prompt.
Trade-offs: The Spark check is model-name-based (`contains "spark"`), so it can route only when the caller includes a Spark identifier in the model string; configured profiles without Spark usage data do not win normal Spark routing, even if their default Codex window has usage left.
Enforcement: `internal/multicodex/exec.go` passes parsed model to `usage.SelectBestAccountForModel`; model parsing and selection tests assert parse flow, Spark weekly selection for configured profiles, and final default-reserve fallback when configured Spark buckets are missing.
References: `internal/multicodex/exec.go`, `internal/multicodex/exec_test.go`, `internal/monitor/usage/model.go`, `internal/monitor/usage/raw_types.go`, `internal/monitor/usage/select.go`, `internal/monitor/usage/select_test.go`, `docs/command-spec.md`

Decision: Keep one weekly monitor card per account and show default and Spark usage inline.
Context: The monitor needs to make weekly usage easy to scan without spending space on duplicate cards or values.
Rationale: One full-width card per account gives the default and Spark lines enough room for percent used, a restrained progress bar, reset countdown, and exact local reset time where useful.
Trade-offs: Optional decoration is hidden in narrow terminals so core weekly values and countdowns remain readable.
Enforcement: `internal/monitor/tui/model.go` renders one weekly card per account, adds Spark as a second line when present, and drops progress and exact time before core values at narrow widths. Layout tests cover narrow, standard, wide, short, many-account, stale, loading, partial, error, color, and no-color states.
References: `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `internal/monitor/usage/model.go`, `docs/command-spec.md`, `docs/implementation-notes.md`

Decision: This public repository keeps always-on public-readiness and safety/privacy/security discipline.
Context: The repository is currently public on GitHub and the user wants public personal repositories to continue following stronger public-surface safety, security, privacy, and publication standards during normal maintenance work.
Rationale: Public repositories have an external audience and external blast radius, so public-readiness hygiene should remain active continuously rather than only during one-off release work.
Trade-offs: Day-to-day maintenance carries more process overhead than it would in a private-only repo.
Enforcement: Keep public-surface safety, security, privacy, and publication checks active for normal maintenance work in this repository.
References: `AGENTS.md`, `docs/workflows.md`, `README.md`

Decision: This personal repository uses only official, reputable, and well-supported third-party dependencies and services by default.
Context: The user explicitly does not want dodgy or non-reputable third-party services, APIs, MCPs, packages, frameworks, libraries, modules, or similar tooling introduced here, regardless of whether the repository is public or private.
Rationale: Favoring official vendor offerings and reputable, popular, well-supported dependencies reduces supply-chain, maintenance, abandonment, and trust risk while keeping the repository easier to maintain.
Trade-offs: Some niche or experimental tools will be skipped unless they later earn a stronger trust/support profile or the user explicitly approves them.
Enforcement: Prefer official APIs, official MCPs, official SDKs, and reputable well-maintained third-party services, packages, frameworks, libraries, and modules. Do not add obscure, weakly maintained, questionable, or low-trust dependencies or integrations without explicit user approval.
References: `docs/decisions.md`

Decision: Plain English and clear naming are the default for this repository.
Context:
The owner wants this repository to stay easy to understand in future chat sessions, docs work, code review, and day-to-day code changes.
Rationale:
Plain English cuts down confusion and makes work faster to read. Clear names in code reduce guessing and make the code easier to change safely later.
Trade-offs:
Some technical ideas need a short extra explanation, and some older names may stay in place until the code around them is touched safely.
Enforcement:
`AGENTS.md` requires plain English in chat and written project material. When touching code, prefer clear descriptive names for files, folders, flags, config keys, functions, classes, types, variables, tests, and examples, and rename confusing names when the change is safe and worth it.
References:
`AGENTS.md`

Decision: Use high-confidence, fail-closed change discipline for repository work.
Context:
Routing, auth, public documentation, and operational command behavior can affect real account usage and public repository safety.
Rationale:
Small, evidence-backed changes are easier to verify and less likely to hide failures or spend protected account quota unexpectedly.
Trade-offs:
Some requests may stop with a clear error instead of continuing through a permissive fallback.
Enforcement:
`docs/workflows.md` defines the active change discipline: make only high-confidence changes, keep one clear current path, avoid speculative fallbacks, treat docs and command contracts as behavior, verify close to the change before broader checks, and keep scratch planning out of committed docs.
References:
`docs/workflows.md`, `AGENTS.md`

Decision: Treat this repository as belonging under the personal GitHub account `olliecrow`.
Context:
Work in this workspace can span personal GitHub accounts and organization-owned repositories. A repo-level ownership note keeps docs, remotes, automation, releases, and publishing steps pointed at the right account.
Rationale:
A clear owner account rule cuts down avoidable confusion and keeps future repo work tied to the right GitHub home.
Trade-offs:
If this repository ever moves to a different owner, this note must be updated in the same change.
Enforcement:
`AGENTS.md` and any repo docs, remotes, automation, release, or publishing steps that need the owning GitHub account should point to `olliecrow` unless Ollie explicitly changes that ownership decision.
References:
`AGENTS.md`

Decision: `multicodex cli` keeps Codex `/goal` state profile-local.
Context:
Codex stores active goal data in the Codex home state database. Multicodex users can run more than one interactive Codex CLI session at the same time from different terminals, each tied to a different account profile.
Rationale:
Binding each `multicodex cli <profile>` process to that profile's `CODEX_HOME` keeps account auth, thread state, and active goal state together. This lets two terminals use different accounts without sharing or overwriting each other's `/goal`.
Trade-offs:
Profiles inherit the shared default `config.toml` unless they use a manual override, so a manual per-profile config must still enable any needed Codex feature flags itself.
Enforcement:
`cmdCLI` repairs the profile home, checks file-backed auth, and launches Codex with the selected profile's `CODEX_HOME`. Tests run two profile-scoped CLI sessions at the same time and assert goal-related state lands in each profile home, not the shared default Codex home.
References:
`internal/multicodex/cli.go`, `internal/multicodex/cli_test.go`, `README.md`, `docs/command-spec.md`
