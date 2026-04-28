# Repository Guidelines

## Repository Ownership
- This repository belongs under the personal GitHub account `olliecrow`.
- Do not move it to a GitHub organization or a different personal account unless Ollie explicitly asks for that change.
- When docs, remotes, automation, releases, or publishing steps need the owning GitHub account, use `olliecrow`.

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
- This repository is public. Treat new repositories as private by default until Ollie explicitly approves making them public.

## Dictation-Aware Input Handling
- The user often dictates prompts, so minor transcription errors and homophone substitutions are expected.
- Infer intent from local context and repository state; ask a concise clarification only when ambiguity changes execution risk.
- Keep explicit typo dictionaries at workspace level (do not duplicate repo-local typo maps).

## Third-Party Dependency Trust Policy
- Prefer official packages, libraries, SDKs, frameworks, and services from authoritative sources.
- Prefer options that are reputable, well-maintained, popular, and well-supported.
- Before adopting or upgrading third-party dependencies, verify ownership/publisher authenticity, maintenance activity, security history, license fit, and ecosystem adoption.
- Avoid low-trust, obscure, or weakly maintained dependencies when a stronger alternative exists.
- Pin versions and keep lockfiles current for reproducibility and supply-chain safety.
- If trust signals are unclear, do not adopt the dependency until explicitly approved.

<!-- third-party-policy:start -->
## Third-Party Repository Handling
- External repositories may be cloned for static analysis only.
- Clone them only into ephemeral `plan/` locations such as `plan/scratch/upstream/` or `plan/artifacts/external/`.
- Immediately sanitize clone metadata: prefer `rm -rf .git`; if `.git` is temporarily needed, remove all remotes first and then remove `.git`.
- Never execute third-party code (no scripts, tests, builds, package installs, binaries, or containers).
- Persistent remotes in this repo must reference only `github.com/olliecrow/*`.
<!-- third-party-policy:end -->

## Plain English Default
- Use plain English in chat, session replies, docs, notes, comments, reports, commit messages, issue text, and review text.
- Prefer short words, short sentences, and direct statements.
- If a technical term is needed for correctness, explain it in simple words the first time.
- In code, prefer clear descriptive names for files, folders, flags, config keys, functions, classes, types, variables, tests, and examples.
- Avoid vague names, short cryptic names, and cute internal code names unless an old established name is already clearer than changing it.
- When touching old code, rename confusing names if the change is low risk and clearly improves readability.
- Keep the durable why for this rule in `docs/decisions.md`.
