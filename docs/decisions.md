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
Rationale: Go provides a static binary, mature stdlib, and simple cross-platform packaging.
Trade-offs: Slightly more verbose than shell scripts, but safer and easier to test.
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
Context: After merging the standalone usage monitor into multicodex, users may still reach for familiar monitor-specific commands such as `completion` and nested help topics.
Rationale: Keeping a small compatibility layer under `multicodex monitor` avoids avoidable dead ends during migration while still steering users toward one unified product.
Trade-offs: Slightly more command-surface and completion/help maintenance.
Enforcement: `multicodex monitor completion [shell]` remains available as a compatibility alias with bash default; help topics and shell completion include nested monitor topics such as `monitor doctor` and `monitor completion`.
References: `internal/multicodex/monitor.go`, `internal/multicodex/help.go`, `internal/multicodex/completion.go`, `internal/multicodex/monitor_test.go`, `README.md`, `docs/command-spec.md`
