# multicodex repository guidance

## Purpose and layout

- `multicodex` is a local-first Go CLI for isolated Codex subscription profiles, automatic `codex exec` routing, heartbeat checks, and usage monitoring.
- `cmd/multicodex/` contains the entry point. Product code and tests live under `internal/`.
- `README.md` is the user guide. `docs/command-spec.md` is the command contract, `docs/security-and-privacy.md` is the security contract, and `docs/decisions.md` records durable cross-cutting rationale.

## Product invariants

- Keep each profile's auth, sessions, threads, `/goal`, and other Codex state inside its profile-local `CODEX_HOME`.
- Never change, copy, restore, back up, symlink, or otherwise manage the shared default Codex auth account. It is only a protected final routing reserve and a read-only monitor source.
- Never print raw credentials or raw subprocess failure output that could contain credentials. Tests and examples must use synthetic state and dummy paths.
- Preserve resource reconciliation's no-clobber behavior: regular profile guidance, config, and skill entries are user overrides; only documented multicodex-owned symlinks may be changed. Runtime-managed `.system` skills remain profile-local.
- Keep usage and routing weekly-only. Prefer declared 10,080-minute windows and retain only the existing narrow compatibility fallback for older provider responses.
- Keep the CLI surface and error behavior aligned with `docs/command-spec.md`.

## Development

- Format Go changes with `gofmt`.
- Run focused tests while iterating, then `go test ./...`, `go test -race ./...`, and `go vet ./...` for material changes.
- Update `README.md` and `docs/command-spec.md` together when user-visible commands, flags, output, or routing behavior changes.
- Keep temporary plans and artifacts in ignored `plan/`; do not commit them.
