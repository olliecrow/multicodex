# Requirements

## Product requirements
- Tool name: `multicodex`
- Commands start with `multicodex`.
- User can create named account profiles.
- User can log in to one or all configured profiles.
- Default behavior must not alter the system default Codex session.
- User can launch Codex CLI or Codex exec with configured profile accounts without switching the system default Codex account.
- Codex exec auto-routing uses weekly usage only and keeps the system default Codex account as the final reserve when no configured profile has usable weekly usage.
- Multicodex does not switch, restore, or manage the default Codex account.
- Status view shows status for all configured profiles.
- User can view live subscription usage across compatible local accounts from the same CLI.
- User can run a fire-and-forget keepalive heartbeat across logged-in profiles.
- Heartbeat must remain profile-local and must not switch the system default Codex account.
- Heartbeat must not persist Codex session files.
- User can run a non-mutating doctor check.
- User can run a monitor-specific read-only doctor check.
- User can run dry-run previews for key operations.
- User can install shell tab-completion for command names and profile names.
- User can optionally inherit profile guidance from one directory and skills from ordered source directories without changing omitted-setting behavior.

## UX requirements
- Simple command names.
- Predictable behavior.
- Clear output showing what changed and what did not change.
- Resource link removal or retargeting reports the previous target.
- Safe defaults with no multicodex commands for global auth effects.

## Technical requirements
- Implementation language: Go.
- Minimal dependencies.
- Atomic file updates for mutable state.
- File permissions locked down for local auth data.
- Robust handling across terminal widths for command output.
- Default persistent multicodex state should live in a single home-level directory (`~/multicodex`) for cross-checkout consistency.
- Heartbeat should be safe for cron use with overlap protection and bounded retry behavior.
- Heartbeat requires an official Codex CLI with `codex exec --ephemeral` support and must fail closed when that capability is unavailable.
- Configured resource paths resolve independently of the process working directory and are validated before profile mutation.

## Compatibility requirements
- Compatible with official Codex CLI login flows.
- No dependence on API-key-only mode for core workflow.
- Preserve regular Codex CLI behavior.
- Profile-scoped Codex launches should inherit current global Codex config by default unless the user explicitly creates a per-profile override.
- Omitting `profile_resources` must leave guidance untouched and preserve the existing strict default skill inheritance path.
- Regular profile guidance and skill entries must remain local overrides and must never be removed by resource reconciliation.
- Resource settings use required booleans and strict nested decoding so misspelled keys cannot silently enable isolation.

## Constraints
- No third-party credential services.
- Do not execute untrusted third-party code while researching.
- Keep this public repository safe for public readers, and keep new repositories private until explicit approval to make them public.
