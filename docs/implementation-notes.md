# Implementation Notes

## Implemented architecture
- Single Go binary with stdlib only.
- Main command router in `main.go` and `app.go`.
- Config and profile state in `config.go` and `paths.go`.
- Command execution and env isolation in `process.go`.
- Global auth switch backup and restore in `switch_global.go`.
- Status inspection in `status.go`.
- Keepalive heartbeat execution in `heartbeat.go`.
- Non-mutating preflight and preview helpers in `doctor.go` and `dry_run.go`.

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
- Manual smoke tests for local and global workflows with temporary homes.
