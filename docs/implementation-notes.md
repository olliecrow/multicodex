# Implementation Notes

## Implemented architecture
- Single Go binary with a small dependency set for the terminal UI.
- Entrypoint in `cmd/multicodex/main.go`.
- Main command router in `internal/multicodex/app.go`.
- Config and profile state in `internal/multicodex/config.go` and `internal/multicodex/paths.go`.
- Command execution and env isolation in `internal/multicodex/process.go`.
- Global auth switch backup and restore in `internal/multicodex/switch_global.go`.
- Status inspection in `internal/multicodex/status.go`.
- Keepalive heartbeat execution in `internal/multicodex/heartbeat.go`.
- Subscription usage monitoring in `internal/monitor/usage` and `internal/monitor/tui`.
- Non-mutating preflight and preview helpers in `internal/multicodex/doctor.go` and `internal/multicodex/dry_run.go`.

## Data layout
- `~/multicodex/config.json` for profile metadata.
- `~/multicodex/profiles/<name>/` for profile-scoped state.
- `~/multicodex/backups/default-auth.backup` for original global auth restore when needed.
- `~/multicodex/heartbeat.lock` for non-overlapping heartbeat runs by default.
- `~/multicodex/monitor/accounts.json` for optional monitor-owned account overrides.
- Legacy `~/.multicodex` is auto-migrated to `~/multicodex` when no explicit `MULTICODEX_HOME` is set.
- Legacy monitor account files remain readable from `~/codex-usage-monitor/accounts.json` and `~/.codex-usage-monitor/accounts.json` when the multicodex monitor file is absent.

## Verification strategy
- Unit tests for config parsing and profile validation.
- Unit tests for environment and command wrapper behavior.
- Unit tests for heartbeat success, failure, timeout, locking, retries, and read-only exec behavior.
- Imported and preserved monitor tests for account discovery, source fetching, observed-token aggregation, and TUI layout stability.
- Unit tests for global switch backup and restore behavior.
- Routine static and race checks with `go vet ./...` and `go test -race ./...`.
- End-to-end battletest harness in isolated temporary homes using a controlled fake `codex` binary for workflow and failure-mode replay.
- Manual smoke tests for local and global workflows with temporary homes.
- Manual verification of newly added real profiles should use a read-only `codex exec` task inside the target profile and then re-check `multicodex status` to confirm the global default did not change.
- Manual verification of heartbeat changes should compare the default/global profile before and after `multicodex heartbeat` to confirm refreshes remain profile-local.
