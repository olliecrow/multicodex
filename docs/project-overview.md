# Project Overview

`multicodex` is a local-first CLI that helps users manage multiple OpenAI Codex subscription accounts on one machine without repeated sign-out and sign-in churn.

Core idea:
- Keep each account isolated in its own local profile context.
- Keep persistent multicodex state in one canonical home-level directory (`~/multicodex`) by default.
- Default to local terminal switching only.
- Support an explicit global switch command when the user wants system-wide default behavior changed.
- Touch the minimum files needed to switch auth context.
- Provide non-mutating `doctor` and `dry-run` helpers for safe setup and operation preview.
- Provide an optional `heartbeat` command for simple periodic keepalive checks across logged-in profiles.
- Provide built-in help topics and shell completion scripts for user-friendly day-to-day usage.

Non-goals:
- No third-party auth proxy.
- No cloud service.
- No token brokering.
- No hidden background daemons.

Related docs:
- Requirements: `docs/requirements.md`
- Command spec: `docs/command-spec.md`
- Security model: `docs/security-and-privacy.md`
- Public repo hygiene: `docs/public-repo-principles.md`
