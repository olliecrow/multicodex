# Command Specification

## Initial command set
- `multicodex init`
- `multicodex add <name>`
- `multicodex login <name>`
- `multicodex login-all`
- `multicodex use <name>`
- `multicodex run <name> -- <command...>`
- `multicodex switch-global <name>`
- `multicodex switch-global --restore-default`
- `multicodex status`
- `multicodex heartbeat`
- `multicodex doctor [--json] [--timeout 8s]`
- `multicodex dry-run [operation]`
- `multicodex completion <bash|zsh|fish>`
- `multicodex help [command]`
- `multicodex version` and `multicodex --version`

## Behavior contract

`multicodex init`
- Creates local multicodex home and metadata only.
- Uses `MULTICODEX_HOME` when set, otherwise defaults to `~/multicodex`.
- If legacy `~/.multicodex` exists and `~/multicodex` does not, it migrates legacy state automatically.
- Does not modify existing Codex default auth.

`multicodex add <name>`
- Registers a named profile.
- Creates a profile directory with secure permissions.

`multicodex login <name>`
- Runs official `codex login` in the profile context.
- Avoids printing sensitive output.

`multicodex login-all`
- Iterates through known profiles and invokes profile-scoped login.
- Summarizes success and failure per profile.

`multicodex use <name>`
- Local scope only.
- Emits shell env instructions or starts a subshell bound to that profile.
- Leaves global default untouched.

`multicodex run <name> -- <command...>`
- Executes one command with profile-scoped context.
- Returns child exit code.

`multicodex switch-global <name>`
- Explicit global operation.
- Changes only minimal auth pointer or file required for default Codex identity.
- Avoids touching unrelated Codex session data.

`multicodex status`
- Shows all profiles and each profile login status.
- Includes which profile is current global default when known.

`multicodex heartbeat`
- Runs a minimal `codex exec --skip-git-repo-check hello` keepalive for each logged-in profile.
- Skips profiles that are currently logged out.
- Prints per-profile result rows and a final summary.
- Returns non-zero when no logged-in profiles are found or when any logged-in profile heartbeat fails.

`multicodex doctor`
- Runs non-mutating setup and auth checks.
- Reports `ok`, `warn`, and `fail` checks with a final pass or fail summary.
- Supports JSON output for automation.
- Includes repository leak-guard checks for:
  - auth homes being outside the active git working tree
  - recommended ignore patterns present in `.gitignore` chain
  - tracked sensitive-looking files (for example `auth.json`, `.env`, and key files)

`multicodex dry-run`
- Prints planned operations without executing commands or mutating files.
- Supports operation-specific previews for:
  - `use <name>`
  - `login <name>`
  - `run <name> -- <command...>`
  - `switch-global <name>`
  - `switch-global --restore-default`

`multicodex completion <shell>`
- Prints tab-completion script for bash, zsh, or fish.
- Supports command name completion.
- Supports dynamic profile-name completion from local multicodex config.

`multicodex help [command]`
- Prints global help when no command topic is provided.
- Prints command-specific usage, description, and examples for one topic.

## Error handling
- Fail fast with actionable messages.
- Never dump secret content in errors.
- Use deterministic exit codes.

## Profile naming
- Profile names may include letters, numbers, `@`, `.`, `_`, and `-`.
- This allows account-like names such as `me@example.com`.
