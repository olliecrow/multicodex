# Repository Guidelines

## Docs, Plans, and Decisions (agent usage)
- `docs/` is long-lived and committed (and may use nested directories + cross-links to stay organized).
- `plan/` is short-lived scratch space and is not committed.
- Decision capture policy lives in `docs/decisions.md`.
- Operating workflow conventions live in `docs/workflows.md`.

## Plan Directory Structure (agent usage)
- `plan/current/`
- `plan/backlog/`
- `plan/complete/`
- `plan/experiments/`
- `plan/artifacts/`
- `plan/scratch/`
- `plan/handoffs/`

## Note routing
- Active notes go in `plan/current/notes.md`.
- Multi-workstream index goes in `plan/current/notes-index.md`.
- Orchestrator packet status goes in `plan/current/orchestrator-status.md`.
- Workflow conventions are documented in `docs/workflows.md`.

## Operating defaults
- Keep changes minimal, explicit, and security first.
- Never commit credentials, tokens, local auth files, or machine-specific private paths.
- Treat this project as private by default until explicit user approval to make it public.

## Dictation-Aware Input Handling
- The user often dictates prompts, so minor transcription errors and homophone substitutions are expected.
- Infer intent from local context and repository state; ask a concise clarification only when ambiguity changes execution risk.
- Keep explicit typo dictionaries at workspace level (do not duplicate repo-local typo maps).
