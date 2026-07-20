# Implementation Notes

## Implemented architecture
- Single Go binary with a small dependency set for the terminal UI.
- Entrypoint in `cmd/multicodex/main.go`.
- Main command router in `internal/multicodex/app.go`.
- Config and profile state in `internal/multicodex/config.go`, `internal/multicodex/resources.go`, and `internal/multicodex/paths.go`.
- Command execution and env isolation in `internal/multicodex/process.go`.
- Status inspection in `internal/multicodex/status.go`.
- Keepalive heartbeat execution in `internal/multicodex/heartbeat.go`.
- Subscription usage monitoring in `internal/monitor/usage` and `internal/monitor/tui`.
- Provider primary/secondary fields are decoded only at the source boundary. The normalized model stores one weekly window for the default bucket and each model-specific bucket.
- Exec selection metadata stores only `weekly_used_percent` for usage telemetry.
- Non-mutating preflight and preview helpers in `internal/multicodex/doctor.go` and `internal/multicodex/dry_run.go`.

## Data layout
- `~/multicodex/config.json` for profile metadata and the optional shared profile-resource policy.
- `~/multicodex/profiles/<name>/` for profile-scoped state.
- `~/multicodex/profiles/<name>/codex-home/config.toml` defaults to a symlink to the default Codex config so profile runs inherit current global settings.
- With omitted resource settings, `~/multicodex/profiles/<name>/codex-home/skills/` uses the original strict default-source reconciliation and guidance remains unmanaged.
- Explicit resource settings resolve `~` from the user home and relative paths from the config directory. Resolution validates every desired source before profile mutation.
- Explicit guidance management reconciles the two Codex guidance names as one override unit. Explicit skill management merges ordered sources with first-source-wins behavior.
- Symlinks at explicitly managed positions are position-owned and may be removed or retargeted with old-target reporting. Regular profile files and directories remain overrides.
- `~/multicodex/heartbeat.lock` for non-overlapping heartbeat runs by default.
- `~/multicodex/monitor/accounts.json` for optional monitor-owned account overrides.

## Verification strategy
- Unit tests for config parsing and profile validation.
- Resource tests cover omitted behavior, strict nested decoding, path forms, missing and wrong-type sources, guidance pair overrides, ordered skill merging, explicit isolation, source changes, foreign and broken symlinks, destination failures before mutation, and old-target reporting.
- Command tests cover policy application in add, login, login-all, CLI, exec, and heartbeat. Exec tests also keep resource notices off Codex's standard output.
- Unit tests for environment and command wrapper behavior.
- Unit tests for interactive CLI handoff into direct `codex` execution.
- Unit tests for command help, status, and unknown commands that must not move local state or rewrite default auth.
- Unit tests for exact file-store config parsing and runtime isolation re-checks after shared-config drift.
- Unit tests for heartbeat success, failure, timeout, locking, retries, and exact ephemeral read-only exec behavior.
- Monitor tests cover weekly normalization, default and Spark routing, observed-token aggregation, stale data, and TUI layout stability across narrow, standard, wide, short, and many-account views.
- Unit tests for profile-local CLI `/goal` state across simultaneous terminals.
- Routine static and race checks with `go vet ./...` and `go test -race ./...`.
- End-to-end battletest harness in isolated temporary homes using a controlled fake `codex` binary for workflow and failure-mode replay.
- Manual smoke tests for profile-local workflows with temporary homes.
- Manual verification of newly added real profiles should use `multicodex status`, an optional read-only prompt in `multicodex cli <name>`, and then `multicodex status` again to confirm profile-local behavior.
- Manual verification of heartbeat changes should confirm refreshes remain profile-local.
