# Global Auth And App Removal

This document records the target change. The implementation still needs to be updated in code, tests, and command docs.

## Decision

Multicodex should stop managing the global Codex account.

The retained product is profile-local CLI and exec routing:
- `multicodex cli <profile>` keeps using that profile's `CODEX_HOME`.
- `multicodex exec ...` keeps selecting and using a configured profile.
- `multicodex run`, `login`, `login-all`, `use`, `heartbeat`, `monitor`, `doctor`, `dry-run`, `completion`, and `help` remain in scope when they do not mutate global auth.

The removed product surface is:
- `multicodex app <profile>`.
- `multicodex switch-global <profile>`.
- `multicodex switch-global --restore-default`.
- Any Multicodex helper that writes, links, backs up, restores, or locks the default Codex `auth.json`.

## Rationale

The app/global-auth path is fragile because it changes shared Codex auth state to influence another process. That creates risk for the normal Codex app, default CLI account, and profile isolation.

The useful Multicodex value is still available without that risk: profile-local CLI and exec calls can run with isolated `CODEX_HOME` values and leave the default Codex account alone.

## Target Invariants

- Multicodex never changes the default Codex auth file or auth pointer.
- The default system Codex account is outside Multicodex control. The owner expects it to be the `crowoy` account, managed through normal Codex login.
- Profile-local commands may read each profile's auth state and may run official Codex commands under profile `CODEX_HOME`.
- Monitor and doctor may inspect local state read-only, but must not imply that Multicodex owns global auth.
- Docs, help, completion, tests, and implementation should present one clear model: local profile routing only.

## Removal Scope

Code to remove or rewrite:
- `internal/multicodex/app_launch.go`.
- `internal/multicodex/switch_global.go`.
- `cmdApp`, `cmdSwitchGlobal`, and `parseSwitchGlobalArgs` routing.
- Global auth backup fields in config, if old config compatibility tests stay safe.
- Status global-default marker logic.
- Dry-run switch-global previews.
- Help and completion entries for `app` and `switch-global`.

Tests to remove or replace:
- App launch tests that protect removed behavior.
- Switch-global tests that protect removed behavior.
- Help, completion, dry-run, status, config, and path tests that expect global switching.
- New tests should assert removed commands are unknown and retained profile-local commands do not touch default auth.

Docs to update after implementation:
- `README.md`.
- `docs/requirements.md`.
- `docs/command-spec.md`.
- `docs/project-overview.md`.
- `docs/implementation-notes.md`.
- `docs/security-and-privacy.md`.
- `docs/decisions.md`.

## Verification

The change is complete only when:
- `go test ./...` passes.
- `go vet ./...` passes.
- Retained `cli` and `exec` tests still cover their full behavior.
- Removed commands are absent from help, completion, README, and command docs.
- Searches find no retained code that mutates default Codex auth.
- Isolated smoke tests show `cli` and `exec` use profile `CODEX_HOME` and leave default auth unchanged.

During active implementation, keep detailed execution notes in `plan/current/` and do not commit them.
