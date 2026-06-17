# Operating Workflow

This document defines how work is tracked so progress compounds without context bloat.

## Core mode
- Keep active notes in `/plan/current/`.
- Promote durable guidance into `/docs/`.
- Capture important rationale in code comments, tests, or docs.
- Keep workflow simple and low ceremony.

## Change discipline
- Make only high-confidence changes that fix observed problems, real risks, broken contracts, failing behavior, or clear quality issues.
- Prefer no change over a weakly justified change.
- Keep scope tight and prefer one clear current path over compatibility shims, backup paths, temporary modes, or old/new dual behavior.
- Treat schemas, status fields, docs, command output, and routing policy as behavior.
- Do not mask failures. If routing, auth, usage, or safety data is unclear, fail with a clear reason instead of guessing.
- Verify close to the change first, then broaden checks when the blast radius is wider.
- Keep scratch planning separate from committed docs, and remove stale scratch artifacts before completion.
- Stage and commit only in-scope files; preserve unrelated changes.

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
- The working tree is clean after committed work, except for explicitly out-of-scope local changes.
