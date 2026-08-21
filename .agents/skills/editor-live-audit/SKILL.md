---
name: editor-live-audit
description: Verify the multicodex editor end to end on local and approved SSH hosts with isolated state, exact builds, real TUI interaction, reconnect checks, and residue-safe cleanup.
---

# Editor live audit

Use this workflow for real multicodex editor checks across the local machine and approved SSH hosts.

1. Inspect repository state, memory pressure, installed build metadata, and active multicodex processes. Record and preserve all pre-existing processes and resources.
2. Build the exact clean repository commit. For cross-host tests, stage that same commit on every host, replace binaries without stopping active editors, monitors, host processes, or terminal sessions, and verify `go version -m` revision and modified state. Restart only an isolated audit editor after all replacements finish.
3. Use a new temporary `MULTICODEX_HOME`. Run the editor with `TMUX` unset. Never use the user's configured editor state for destructive tests.
4. Use only the multicodex repository for Git workspace tests. Use explicit private scratch directories for non-Git remote tests. Never modify another repository.
5. Exercise the real TUI: add hosts and projects, require a workspace name, create the automatic first terminal, run host markers, paste ordinary text, add and rename terminals, use dynamic slots, switch hosts, restart only the outer editor, and verify the selected session, marker, pane process, scrollback, and ownership state are unchanged.
6. Exercise deletion and cleanup through the editor. Verify confirmation text, altered-resource preservation, cascade deletion, reconnect-selection cleanup, Git branch and worktree cleanup, attachments, tmux sessions, control sockets, and host processes.
7. Compare exact owned resources before and after the audit. Remove only validated temporary resources with the test instance identity or exact scratch prefix. Stop when ownership is uncertain.
8. Run focused tests, repository-required gates, public outgoing-change checks, installation verification, and remote CI for the exact pushed commit.

Keep captured output free of credentials and private account data. Report any pre-existing process that ended naturally; never replace it merely to test a new build.
