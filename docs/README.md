# Docs Directory

This directory holds long-term, agent-focused documentation for this repo. It is not intended for human readers and is committed to git.

Principles:
- Keep content evergreen and aligned with the codebase.
- Avoid time- or date-dependent language.
- Prefer updating existing docs when they have a clear home, but do not hesitate to create new focused docs and nested subdirectories when it improves organization and findability.
- Use docs for cross-cutting context or rationale that does not belong in code comments or tests.
- Keep entries concise and high-signal.
- Make docs interrelate: use relative links between related docs and avoid orphan docs by linking new docs from an index or a nearby parent doc.

Relationship to `/plan/`:
- `/plan/` is a short-term, disposable scratch space for agents and is not committed to git.
- `/plan/handoffs/` is used for sequential handoff summaries for staged automation workflows.
- Active notes should be routed into `/plan/current/` and promoted into `/docs/` only when they become durable guidance.
- `/docs/` is long-lived. Only stable guidance should live here.
