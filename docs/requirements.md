# Requirements

## Product requirements
- Tool name: `multicodex`
- Commands start with `multicodex`.
- User can create named account profiles.
- User can log in to one or all configured profiles.
- Default behavior must not alter the system default Codex session.
- User can switch account for current terminal only.
- User can explicitly switch global default Codex account.
- Status view shows status for all configured profiles.
- User can run a fire-and-forget keepalive heartbeat across logged-in profiles.
- User can run a non-mutating doctor check.
- User can run dry-run previews for key operations.
- User can install shell tab-completion for command names and profile names.

## UX requirements
- Simple command names.
- Predictable behavior.
- Clear output showing what changed and what did not change.
- Safe defaults with explicit commands for global effects.

## Technical requirements
- Implementation language: Go.
- Minimal dependencies.
- Atomic file updates for mutable state.
- File permissions locked down for local auth data.
- Robust handling across terminal widths for command output.
- Default persistent multicodex state should live in a single home-level directory (`~/multicodex`) for cross-checkout consistency.

## Compatibility requirements
- Compatible with official Codex CLI login flows.
- No dependence on API-key-only mode for core workflow.
- Preserve compatibility with Codex app and regular CLI behavior.

## Constraints
- No third-party credential services.
- Do not execute untrusted third-party code while researching.
- Default private project posture until explicit approval to make public.
