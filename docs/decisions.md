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

Decision: Keep account switching local-first and minimal-touch.
Context: User wants local shell switching by default and careful handling of global defaults.
Rationale: Minimize side effects and reduce risk to unrelated Codex state.
Trade-offs: More explicit commands, less hidden magic.
Enforcement: `multicodex use` only changes current shell context. `multicodex switch-global` is explicit.
References: `docs/requirements.md`, `docs/command-spec.md`

Decision: Never handle raw secrets directly in multicodex internals unless unavoidable.
Context: Strong privacy and confidentiality requirements.
Rationale: Lower blast radius and simplify trust model for open-source readiness.
Trade-offs: Some actions delegate to official `codex login` flows.
Enforcement: No secret logging, strict file permissions, secret-safe tests and fixtures.
References: `docs/security-and-privacy.md`

Decision: Ship explicit `doctor` and `dry-run` helper commands.
Context: Similar user-facing repos use non-mutating preflight and preview helpers to reduce setup confusion and avoid accidental side effects.
Rationale: Users can validate environment and understand exactly what commands would do before running mutating operations.
Trade-offs: More command surface area, but lower operational risk and better onboarding.
Enforcement: `doctor` and `dry-run` implementations are non-mutating and covered by tests.
References: `internal/multicodex/doctor.go`, `internal/multicodex/dry_run.go`, `README.md`, `docs/command-spec.md`

Decision: Allow account-like profile names with `@`.
Context: Users may naturally use account identifiers such as email-like names for profiles.
Rationale: Better usability with minimal additional risk because path-unsafe separators remain disallowed.
Trade-offs: Slightly broader allowed character set.
Enforcement: Validation regex and tests.
References: `internal/multicodex/validate.go`, `internal/multicodex/validate_test.go`

Decision: `status` should extract account email from local profile auth when CLI output does not include it.
Context: `codex login status` does not always print account email, which made status output less useful.
Rationale: Reading email claim from local `id_token` gives consistent user-facing account identification.
Trade-offs: Additional local parsing logic for JWT payload.
Enforcement: Status helper and unit tests.
References: `internal/multicodex/status.go`, `internal/multicodex/status_test.go`

Decision: Restoring global default auth from file backup must replace symlink with a regular file.
Context: Battle tests found restore could leave `~/.codex/auth.json` as a symlink while only changing target content.
Rationale: Restore should return file shape and contents to original state, not only payload bytes.
Trade-offs: One additional remove step before write.
Enforcement: Restore path update and test asserting regular-file restoration.
References: `internal/multicodex/switch_global.go`, `internal/multicodex/switch_global_test.go`

Decision: Add doctor leak guards and auth-permission normalization.
Context: Users need confidence that auth details are handled safely and do not get committed.
Rationale: Proactive checks for repo leakage plus post-login `0600` permissions reduce accidental disclosure risk.
Trade-offs: Slightly more checks and warnings in doctor output.
Enforcement: Doctor checks for path isolation, ignore coverage, and tracked sensitive files; tests cover helper logic.
References: `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`, `internal/multicodex/security.go`, `internal/multicodex/security_test.go`

Decision: Normalize path comparisons for leak guards using symlink-aware canonicalization.
Context: On macOS, equivalent paths may appear as `/var/...` and `/private/var/...`, causing false negatives in subpath checks.
Rationale: Canonicalizing through existing parent symlinks avoids bypasses and ensures path isolation checks are reliable.
Trade-offs: Slightly more path-resolution logic.
Enforcement: Canonical path helper plus subpath tests with symlink aliases.
References: `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`

Decision: Bound profile status latency with per-call timeout and parallel profile checks.
Context: Battle tests showed `status` and `doctor` could become slow with multiple profiles or hanging `codex login status` calls.
Rationale: Timeout plus bounded parallelism keeps CLI responsive while preserving deterministic output ordering.
Trade-offs: More concurrent subprocesses and slightly more code complexity.
Enforcement: Timeout handling in status logic, worker-limited parallel collection, and timeout regression tests.
References: `internal/multicodex/status.go`, `internal/multicodex/status_timeout_test.go`, `internal/multicodex/doctor.go`

Decision: Add a profile-scoped `heartbeat` keepalive command using minimal Codex calls.
Context: Users want a simple fire-and-forget way to keep subscription windows active and verify each logged-in profile still works.
Rationale: Running `codex exec --skip-git-repo-check hello` inside each profile context is simple, independent, and compatible with official auth flows.
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

Decision: Default persistent multicodex state to `~/multicodex` with automatic migration from legacy `~/.multicodex`.
Context: Users may run multiple checkouts and worktrees; one stable home-level state directory reduces fragmentation and accidental repo-local storage.
Rationale: A single predictable directory improves safety and operational consistency, and migration preserves existing user state without manual steps.
Trade-offs: Existing users on the old default path incur a one-time filesystem move when no explicit `MULTICODEX_HOME` is set.
Enforcement: `ResolvePaths` defaults to `~/multicodex`, performs legacy migration when safe, and tests cover defaulting, migration, and explicit override behavior.
References: `internal/multicodex/paths.go`, `internal/multicodex/paths_test.go`, `README.md`, `docs/implementation-notes.md`

Decision: Use Go `cmd/` and `internal/` layout for public-facing maintainability while preserving behavior.
Context: The initial implementation was flat in the repo root and had become harder to scan as command surface and checks expanded.
Rationale: `cmd/multicodex` for entrypoint and `internal/multicodex` for implementation aligns with common Go conventions and improves contributor onboarding without changing user-visible behavior.
Trade-offs: File moves add short-term churn in docs and references.
Enforcement: Entrypoint lives in `cmd/multicodex/main.go`; implementation and tests live in `internal/multicodex`; battletest plus unit/race/vet checks validate parity after refactor.
References: `cmd/multicodex/main.go`, `internal/multicodex/`, `README.md`, `docs/implementation-notes.md`

Decision: Prefer targeted multicodex state ignore patterns over broad `multicodex/` path ignores.
Context: After introducing `internal/multicodex`, a broad `multicodex/` ignore rule risked masking source directories and weakening review safety.
Rationale: Explicit patterns for `config.json`, `profiles/`, and `backups/` retain secret-safety goals without accidentally hiding tracked source files.
Trade-offs: Slightly longer ignore patterns and doctor guidance.
Enforcement: `.gitignore` uses targeted patterns; doctor missing-pattern checks accept legacy `.multicodex/` or targeted `multicodex` state patterns; tests assert coverage.
References: `.gitignore`, `internal/multicodex/doctor.go`, `internal/multicodex/doctor_test.go`, `docs/security-and-privacy.md`

Decision: Verify newly added profiles with a real read-only Codex request and a follow-up default-profile check.
Context: `codex login status` proves auth is present, but it does not prove the profile can complete an actual request or that profile-scoped execution left the default/global profile untouched.
Rationale: A small read-only `codex exec` smoke test catches broken profile wiring and accidental global-switch regressions with minimal risk.
Trade-offs: Uses a small amount of real model usage during verification.
Enforcement: For manual profile verification, run `multicodex run <name> -- codex exec -s read-only -C <repo> ...` and confirm `multicodex status` reports the same global default before and after the test.
References: `README.md`, `docs/implementation-notes.md`, `docs/command-spec.md`

Decision: Make heartbeat cron-safe with local locking, bounded retries, and read-only execution.
Context: Scheduled keepalive runs should not overlap, should tolerate transient failures, and should never need to mutate the current workspace or the global default Codex account.
Rationale: A local OS lock avoids duplicate overlapping work, one retry with linear backoff handles short-lived provider hiccups, and forcing `codex exec` into read-only mode reduces accidental side effects during automated refresh runs.
Trade-offs: Slightly more heartbeat code and a small delay before final failure when retries are used.
Enforcement: `multicodex heartbeat` acquires a non-blocking lock under multicodex home, retries failed profile heartbeats, runs `codex exec` with `--sandbox read-only`, and keeps all auth routing profile-scoped via `CODEX_HOME`.
References: `internal/multicodex/heartbeat.go`, `internal/multicodex/heartbeat_test.go`, `README.md`, `docs/command-spec.md`

Decision: Fold subscription usage monitoring into multicodex under a namespaced `monitor` command.
Context: Users choose between multiple Codex accounts based on both account isolation and remaining subscription headroom, so keeping switching and monitoring in separate products created an avoidable split workflow.
Rationale: One product with a dedicated `monitor` namespace matches the real user workflow while keeping usage visibility clearly separated from mutating account-management commands.
Trade-offs: The repo and CLI gain more code and dependencies, so the monitor must stay modular and avoid bloating the root command surface.
Enforcement: The integrated monitor lives under `internal/monitor/`; the primary user entrypoint is `multicodex monitor`; monitor account discovery prefers multicodex profiles and `~/multicodex/monitor/accounts.json`, with legacy monitor account-file paths retained as compatibility fallbacks.
References: `internal/multicodex/monitor.go`, `internal/monitor/usage/accounts.go`, `internal/monitor/tui/model.go`, `README.md`, `docs/command-spec.md`, `docs/implementation-notes.md`

Decision: Preserve standalone monitor command habits where they materially reduce migration friction.
Context: After merging the standalone usage monitor into multicodex, users may still reach for familiar monitor-specific commands such as `completion`, explicit monitor UI aliases, and nested help topics.
Rationale: Keeping a small compatibility layer under `multicodex monitor` avoids avoidable dead ends during migration while still steering users toward one unified product.
Trade-offs: Slightly more command-surface and completion/help maintenance.
Enforcement: `multicodex monitor completion [shell]` remains available as a compatibility alias with bash default; explicit UI alias `multicodex monitor tui` remains help-addressable; help topics and shell completion include nested monitor topics such as `monitor doctor`, `monitor completion`, and `monitor tui`.
References: `internal/multicodex/monitor.go`, `internal/multicodex/help.go`, `internal/multicodex/completion.go`, `internal/multicodex/monitor_test.go`, `README.md`, `docs/command-spec.md`

Decision: Default profile config to the shared global Codex config, while preserving explicit per-profile overrides.
Context: Users expect Codex feature settings such as search or model defaults to stay consistent across regular Codex usage and multicodex profile usage without copying config files into each profile.
Rationale: A profile-local symlink to the default Codex `config.toml` keeps settings current automatically as the global config changes, while leaving any non-generated profile-local config file intact preserves an escape hatch for account-specific customization.
Trade-offs: Auth isolation now depends more directly on the default Codex config using file-backed credentials; profile login must fail clearly when the effective config would not use file-backed auth; doctor output must explain shared-config states clearly.
Enforcement: New profiles create a `config.toml` symlink to the default Codex config; existing autogenerated profile configs are migrated to that symlink; manually maintained profile config files are preserved as overrides; `multicodex login` and profile-scoped Codex execution paths reject configs that do not enable file-backed auth.
References: `internal/multicodex/config.go`, `internal/multicodex/config_test.go`, `internal/multicodex/doctor.go`, `README.md`, `docs/implementation-notes.md`

Decision: Present monitor identities and timestamps for operator readability while keeping internal timekeeping canonical.
Context: The monitor aggregates account usage across multiple Codex homes, but raw email addresses and UTC timestamps with seconds make the TUI harder to scan during live account selection.
Rationale: Showing configured account labels instead of emails keeps the UI aligned with the names users chose in multicodex, and rendering user-facing timestamps in local time at minute precision improves readability without changing internal UTC tracking.
Trade-offs: Duplicate or missing account labels can still make the display ambiguous, so the UI falls back to stable account or user IDs when labels are unavailable; local rendering means screenshots differ across operator time zones.
Enforcement: `internal/monitor/tui/model.go` renders window titles and account summaries from labels first, keeps active-account matching keyed off stable identity fields, and formats user-facing timestamps in local time without seconds; `internal/monitor/tui/model_test.go` asserts label-first titles and local-time header/reset formatting.
References: `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Add `multicodex exec` as an auto-routing wrapper around `codex exec`.
Context: Users often want the convenience of `codex exec` without manually choosing which logged-in subscription account currently has the most weekly headroom.
Rationale: A dedicated `multicodex exec` command preserves a simple, familiar interface while keeping account-selection policy explicit and local to multicodex.
Trade-offs: Selection is still best-effort snapshot routing, so simultaneous launches can choose the same profile and the chosen account may change between invocations.
Enforcement: `multicodex exec` forwards all arguments directly to `codex exec`, bypasses profile selection for help requests, treats configured profiles below 40% five-hour usage as eligible, picks the eligible profile whose weekly reset is soonest, falls back to a random eligible profile when the weekly reset is unknown for all eligible profiles, falls back to a random accessible profile when none are eligible, and uses a random configured profile when usage data is unavailable for every profile.
References: `internal/multicodex/exec.go`, `internal/multicodex/exec_test.go`, `internal/monitor/usage/select.go`, `internal/monitor/usage/select_test.go`, `README.md`, `docs/command-spec.md`

Decision: Launch Codex Desktop with shared app state and per-launch profile auth selection.
Context: Users want more than one Codex Mac app window at the same time, with one shared left-side project list and the ability to choose which profile each new app window starts with.
Rationale: On macOS, `open -n -a` can launch separate `Codex.app` processes, and the launched app process keeps the shell environment, including `CODEX_HOME`. Later investigation showed Codex keeps sidebar and thread state under `CODEX_HOME`, so separate profile homes split the left-side project list. Switching the shared global auth pointer first, then launching the app with the shared default `CODEX_HOME`, is the simplest way to keep one shared sidebar while still choosing the account used at launch time.
Trade-offs: This command is macOS-only, depends on an installed `Codex.app`, and separate app processes can still consume usage independently at the same time. Account split is best-effort rather than a hard wall because Codex caches auth on startup but can reload auth later in some flows.
Enforcement: `multicodex app <name>` is macOS-only, switches the shared default auth pointer to the selected profile, launches `Codex.app` via `open -n -a` with the shared default `CODEX_HOME`, and reuses the same file-backed-auth isolation checks as `switch-global`.
References: `internal/multicodex/app_launch.go`, `internal/multicodex/app_launch_test.go`, `internal/multicodex/help.go`, `internal/multicodex/completion.go`, `README.md`, `docs/command-spec.md`

Decision: Parse `cli_auth_credentials_store` by exact key instead of substring matching.
Context: Shared profile configs rely on the default Codex `config.toml`, so auth-isolation checks must inspect the real credential-store setting rather than unrelated comments or strings.
Rationale: A small exact-key parser removes false positives without adding a TOML dependency and keeps login, doctor, exec, heartbeat, and switch-global checks aligned.
Trade-offs: Slightly more parsing code to maintain, but much lower risk of silently misclassifying auth isolation.
Enforcement: All file-store checks route through the shared parser and regression tests cover comments, unrelated strings, and nested tables.
References: `internal/multicodex/config.go`, `internal/multicodex/config_test.go`, `internal/multicodex/doctor.go`, `internal/multicodex/app.go`

Decision: Refresh global restore metadata when default auth changes outside multicodex.
Context: Users may log into the default Codex home outside multicodex after an earlier switch, and restore should not send them back to stale historical auth.
Rationale: Preserving the latest non-multicodex-managed default auth state keeps restore safe while still preserving the original external state across profile-to-profile multicodex switching.
Trade-offs: Backup logic is more stateful than a one-time snapshot, but it better matches real operator expectations and avoids deleting newer default auth.
Enforcement: `switch-global` refreshes backup metadata only when the current default auth is not a multicodex-managed profile symlink and differs from the stored backup; restore tests cover external changes and profile-to-profile switching.
References: `internal/multicodex/switch_global.go`, `internal/multicodex/switch_global_test.go`, `README.md`, `docs/implementation-notes.md`

Decision: Keep monitor doctor usable with fallback while surfacing degraded state explicitly.
Context: The monitor has both an app-server source and an OAuth fallback, and operators need to distinguish "fully healthy" from "usable with one source down".
Rationale: Preserving exit-success when one source works avoids breaking automation, while explicit degraded reporting makes primary-path failures visible.
Trade-offs: Human output gains one more summary state to interpret.
Enforcement: `monitor doctor` reports PASS, PASS (degraded), or FAIL based on per-source checks, while exit status still succeeds when at least one source works.
References: `internal/monitor/usage/doctor.go`, `internal/multicodex/monitor.go`, `README.md`, `docs/command-spec.md`

Decision: Treat the default auth symlink target as an active-home alias in the monitor.
Context: In common multicodex setups, `~/.codex/auth.json` is a symlink into the currently selected profile home, so the logical active account may be reachable through both `~/.codex` and `~/multicodex/profiles/<name>/codex-home`.
Rationale: Matching only the `~/.codex` directory can blank the active window cards when the default alias row fails even though the linked profile row succeeds in the same refresh.
Trade-offs: Active-account detection now follows one extra filesystem indirection through `auth.json`, which slightly increases coupling to the file-backed-auth setup already required by multicodex.
Enforcement: Monitor active-account selection matches both the active Codex home and the resolved directory of its `auth.json` symlink target; regression tests cover the alias case.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`

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

Decision: Active-account alias rows prefer the real profile label over the synthetic `default` alias.
Context: When `~/.codex/auth.json` is linked into a multicodex profile home, the monitor can discover the same logical account twice: once via the synthetic `default` row and once via the real profile row.
Rationale: Surfacing the synthetic `default` label for that deduplicated account makes the active window cards disagree with `multicodex status` and obscures which profile is actually selected.
Trade-offs: The monitor now treats the profile row as the canonical display identity for an active alias pair, even if the synthetic `default` row was encountered later in the refresh.
Enforcement: Active-account label selection and deduplicated account-row selection both prefer non-synthetic profile labels over the `default` alias when they resolve to the same active account; regression tests cover healthy and degraded alias cases.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`

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
Enforcement: Summary-level observed token estimates add same-identity home totals instead of taking the maximum; the TUI labels these values as token estimates and shows `partial` directly when some home estimates are missing.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Prefer a plain-English re-login warning when monitor fetches fail because a profile token expired.
Context: The monitor can detect expired profile auth from both the app-server path and the oauth fallback, but the raw provider error is long and easy to miss in the TUI diagnostics line.
Rationale: A short warning such as `account "work" auth expired; sign in again` tells the operator what to do next without hiding the underlying failure from deeper debug output.
Trade-offs: The top-level warning is less literal than the raw provider response, so the account row still keeps the original error text for debugging and tests.
Enforcement: Multi-account monitor summaries collapse token-expired fetch errors into plain-English account warnings, and the TUI diagnostics priority prefers those re-login warnings ahead of generic account fetch failures when no active-window warning is present.
References: `internal/monitor/usage/fetcher.go`, `internal/monitor/usage/fetcher_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Accounts with only one official usage window stay visible in the monitor.
Context: Some accounts return a valid five-hour official usage window but omit the weekly window entirely. Treating the missing weekly window as a full fetch failure makes the whole account look broken even when real usage data is present.
Rationale: Operators need to see the usable window data that does exist. Marking only the missing side as unavailable matches the provider response more closely and avoids hiding healthy account data.
Trade-offs: The TUI can now show mixed availability inside one account row, so readers must understand that one window can be present while the other is missing.
Enforcement: Usage normalization accepts a missing secondary window, stores it as unavailable instead of failing the whole summary, and the TUI renders availability per window panel rather than per account row. Tests cover both the normalization path and the mixed-availability display.
References: `internal/monitor/usage/raw_types.go`, `internal/monitor/usage/raw_types_test.go`, `internal/monitor/tui/model.go`, `internal/monitor/tui/model_test.go`, `README.md`, `docs/command-spec.md`

Decision: Default-branch-first day-to-day workflow is acceptable in this personal repo.
Context: This repository is part of the user's personal GitHub portfolio and often supports experimental or fast-iteration work. The user explicitly prefers to work directly on the default branch for normal day-to-day changes unless there is a task-specific reason to branch.
Rationale: Working directly on the default branch keeps personal-repo execution simple and fast. Branches remain available when they materially help with coordination, isolation, or review.
Trade-offs: There is less branch isolation by default, so targeted staging, small checkpoints, and verification still matter.
Enforcement: Agents may use the repository's default branch for normal personal-repo work unless the user requests a separate branch or the task clearly benefits from one.
References: `AGENTS.md`, `docs/workflows.md`, `README.md`

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
