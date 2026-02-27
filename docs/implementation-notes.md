# Implementation Notes

## Implemented architecture
- Single Go binary with stdlib only.
- Entrypoint in `cmd/multicodex/main.go`.
- Main command router in `internal/multicodex/app.go`.
- Config and profile state in `internal/multicodex/config.go` and `internal/multicodex/paths.go`.
- Command execution and env isolation in `internal/multicodex/process.go`.
- Global auth switch backup and restore in `internal/multicodex/switch_global.go`.
- Status inspection in `internal/multicodex/status.go`.
- Keepalive heartbeat execution in `internal/multicodex/heartbeat.go`.
- Non-mutating preflight and preview helpers in `internal/multicodex/doctor.go` and `internal/multicodex/dry_run.go`.

## Data layout
- `~/multicodex/config.json` for profile metadata.
- `~/multicodex/profiles/<name>/` for profile-scoped state.
- `~/multicodex/backups/default-auth.backup` for original global auth restore when needed.
- Legacy `~/.multicodex` is auto-migrated to `~/multicodex` when no explicit `MULTICODEX_HOME` is set.

## Verification strategy
- Unit tests for config parsing and profile validation.
- Unit tests for environment and command wrapper behavior.
- Unit tests for heartbeat success, failure, and timeout behavior.
- Unit tests for global switch backup and restore behavior.
- Routine static and race checks with `go vet ./...` and `go test -race ./...`.
- End-to-end battletest harness in isolated temporary homes using a controlled fake `codex` binary for workflow and failure-mode replay.
- Manual smoke tests for local and global workflows with temporary homes.
