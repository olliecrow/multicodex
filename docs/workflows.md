# Operating Workflow

This document defines how work is tracked so progress compounds without context bloat.

## Core mode
- Keep active notes in `/plan/current/`.
- Promote durable guidance into `/docs/`.
- Capture important rationale in code comments, tests, or docs.
- Keep workflow simple and low ceremony.

## Note routing
- `/plan/current/notes.md`: running notes and immediate next actions.
- `/plan/current/notes-index.md`: compact index of active workstreams.
- `/plan/current/orchestrator-status.md`: packet and status board for parallel work.
- `/plan/handoffs/`: handoff summaries for staged workflows.

## Parallel and subagent workflows
- Split work only when there is a clear speed or quality win.
- Track each stream with owner, scope, status, blockers, and last update.
- Require concise handoff summaries before integrating.

## Promotion cycle
- During implementation: write short notes to `/plan/current/`.
- At milestones: de-duplicate notes and promote durable learnings into `/docs/`.
- Before completion: remove stale scratch artifacts from `/plan/`.

## Stop conditions
- Acceptance checks pass.
- Risks are documented.
- No unresolved blockers remain.
