# Implementation Notes

## Implemented architecture
- Single Go binary with a small dependency set for the terminal UI.
- Entrypoint in `cmd/multicodex/main.go`.
- Main command router in `internal/multicodex/app.go`.
- Config and profile state in `internal/multicodex/config.go` and `internal/multicodex/paths.go`.
- Command execution and env isolation in `internal/multicodex/process.go`.
- Status inspection in `internal/multicodex/status.go`.
- Keepalive heartbeat execution in `internal/multicodex/heartbeat.go`.
- Subscription usage monitoring in `internal/monitor/usage` and `internal/monitor/tui`.
- Non-mutating preflight and preview helpers in `internal/multicodex/doctor.go` and `internal/multicodex/dry_run.go`.

## Data layout
- `~/multicodex/config.json` for profile metadata.
- `~/multicodex/profiles/<name>/` for profile-scoped state.
- `~/multicodex/profiles/<name>/codex-home/config.toml` defaults to a symlink to the default Codex config so profile runs inherit current global settings.
- `~/multicodex/profiles/<name>/codex-home/skills/` fills in missing top-level entries from the default Codex skills tree while keeping manual profile overrides.
- `~/multicodex/heartbeat.lock` for non-overlapping heartbeat runs by default.
- `~/multicodex/monitor/accounts.json` for optional monitor-owned account overrides.

## Verification strategy
- Unit tests for config parsing and profile validation.
- Unit tests for environment and command wrapper behavior.
- Unit tests for interactive CLI handoff into direct `codex` execution.
- Unit tests for command help, status, and unknown commands that must not move local state or rewrite default auth.
- Unit tests for exact file-store config parsing and runtime isolation re-checks after shared-config drift.
- Unit tests for heartbeat success, failure, timeout, locking, retries, and read-only exec behavior.
- Imported and preserved monitor tests for account discovery, source fetching, observed-token aggregation, and TUI layout stability.
- Unit tests for profile-local CLI `/goal` state across simultaneous terminals.
- Routine static and race checks with `go vet ./...` and `go test -race ./...`.
- End-to-end battletest harness in isolated temporary homes using a controlled fake `codex` binary for workflow and failure-mode replay.
- Manual smoke tests for profile-local workflows with temporary homes.
- Manual verification of newly added real profiles should use `multicodex status`, an optional read-only prompt in `multicodex cli <name>`, and then `multicodex status` again to confirm profile-local behavior.
- Manual verification of heartbeat changes should confirm refreshes remain profile-local.
