---
name: multicodex-editor-live-audit
description: Verify the multicodex editor end to end on local and approved SSH hosts with isolated state, exact builds, real TUI interaction, reconnect checks, and residue-safe cleanup.
---

# Editor live audit

Use this workflow for real multicodex editor checks across the local machine and approved SSH hosts.

1. Inspect repository state, memory pressure, installed build metadata, and active multicodex processes. Record and preserve all existing processes and resources.
2. Build and install the exact clean repository commit.
   - Detect each host's operating system and architecture. Build once for each distinct target. Never copy a client binary to a different target.
   - Resolve each installed command through symbolic links. Inspect the real executable and its parent directory. A Linux link's displayed `0777` mode does not describe the target's permissions.
   - If the current user owns and can write the executable and parent directory, stage the matching binary beside the target. Verify it, set mode `0755`, and atomically rename it over the target while preserving the link.
   - Stop and request authority if the real target is not safely writable. Do not overwrite through the link or infer permission from the link mode.
   - Replace binaries without stopping active editors, monitors, host processes, or terminal sessions. Verify file format and the `go version -m` revision and modified state.
   - Restart only an isolated audit editor after all replacements finish.
   - Run post-install TUI checks through the installed `multicodex editor` command from `PATH`, exactly as a user starts it. A build-artifact check is not enough.
3. Use a new temporary `MULTICODEX_HOME`. Run the editor with `TMUX` unset. Never use the user's configured editor state for destructive tests.
4. Use only the multicodex repository for Git workspace tests. Use explicit private scratch directories for non-Git remote tests. Never modify another repository.
5. Exercise the real TUI with mouse and keyboard at the supported minimum size and a realistic working size.
   - Verify that users can distinguish projects, workspaces, windows, selection, focus, and terminal state. Labels must not clip. Instructions and mouse targets must work. Spacing must stay stable, and states must remain legible without color. Do not rely on test names or stripped text. Except for the fixed-width pulse, refreshes must not blink, move the layout, or replace stable health text.
   - Add hosts and projects. Click a project and verify that its terminal opens in the exact original directory without changing Git worktrees. Reopen it and verify that the same session returns.
   - Use `Ctrl+N` on the project. Require a workspace name and create the first workspace terminal. Paste ordinary text, add and rename workspace terminals, use dynamic slots, and switch hosts.
   - Keep unselected windows in two projects changing at alternating times and another window quiet. Verify that rows stay name-ordered, changed output shows `●` for 30 seconds, and unchanged output then shows `○` without reordering.
   - Verify that focus and process-name changes do not renew the signal. A project without a direct terminal must show `◇`. The sidebar title must keep stable live health text. Stopped and offline windows must show `×` and `?`.
   - Type `exit` in one isolated editor-created terminal. Verify that the window disappears promptly and its project or workspace remains. Separately verify that a missing session appears as stopped and is not removed automatically.
   - Verify the visible MacBook controls without Home, Page Up, Page Down, or function keys. Test arrows, Option-arrow paging, Command-arrow edges, Help, create, rename, cancel, and their displayed fallbacks.
   - Upload one synthetic attachment. Verify its digest and unsubmitted terminal draft, then clear the draft.
   - For Git, verify the exact worktree lock. Switch to a second branch, reconnect, and confirm that deletion preserves the second branch.
   - Test adoption through the backend integration path with an exact temporary tmux socket and synthetic session. Verify eligibility, stable session and pane identifiers, exact state markers, cleanup, release, and unchanged process state. Never adopt a user's session during an audit.
   - When the user explicitly authorizes real Codex calls, run one harmless read-only `multicodex exec` prompt inside a terminal on each host. Capture output in private scratch files. Verify only the exit status and an exact synthetic reply. Do not display routing or account diagnostics. Preserve profile-local Codex state.
6. Saturate tmux history and verify the pane retains at least the documented minimum effective history. Do not treat the configured `history-limit` alone as proof. Enter and leave scrollback, terminate only an isolated attachment transport, and restart only the isolated outer editor. Verify the selected session, marker, pane process, history, and ownership state are unchanged after each recovery.
7. Exercise deletion and cleanup through the editor. Verify confirmation text, altered-resource preservation, cascade deletion, reconnect-selection cleanup, explicit Git branch and worktree deletion, attachments, tmux sessions, control sockets, and host processes. Prove that automatic cleanup reports a stale Git workspace without removing its worktree or branch.
8. Compare exact owned resources before and after the audit. Remove only validated temporary resources and empty isolated registries with the test instance identity or exact scratch prefix. Stop when ownership is uncertain.
9. Run focused tests, repository-required gates, public outgoing-change checks, installation verification, and remote CI for the exact pushed commit.

Keep captured output free of credentials and private account data. Report any pre-existing process that ended naturally; never replace it merely to test a new build.
