# Command Specification

## Initial command set
- `multicodex init`
- `multicodex add <name>`
- `multicodex login <name>`
- `multicodex login-all`
- `multicodex use <name>`
- `multicodex run <name> -- <command...>`
- `multicodex exec [codex exec args]`
- `multicodex switch-global <name> [--force]`
- `multicodex switch-global --restore-default`
- `multicodex status`
- `multicodex heartbeat`
- `multicodex monitor [flags]`
- `multicodex monitor help`
- `multicodex monitor tui [flags]`
- `multicodex monitor doctor [--json] [--timeout 20s]`
- `multicodex monitor completion [shell]`
- `multicodex doctor [--json] [--timeout 8s]`
- `multicodex dry-run [operation]`
- `multicodex completion <bash|zsh|fish>`
- `multicodex help [command [subcommand]]`
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
- Defaults the profile `config.toml` to the current default Codex `config.toml` so shared settings stay in sync.
- Leaves a manual per-profile `config.toml` override intact when present.

`multicodex login <name>`
- Runs official `codex login` in the profile context.
- Avoids printing sensitive output.
- Requires the effective profile config to enable `cli_auth_credentials_store = "file"` so profile auth remains isolated.

`multicodex login-all`
- Iterates through known profiles and invokes profile-scoped login.
- Summarizes success and failure per profile.

`multicodex use <name>`
- Local scope only.
- Emits shell env instructions or starts a subshell bound to that profile.
- Leaves global default untouched.

`multicodex run <name> -- <command...>`
- Executes one command with profile-scoped context.
- Re-checks file-backed auth isolation before direct `codex` invocations.
- Returns child exit code.

`multicodex exec [codex exec args]`
- Executes `codex exec` with all remaining arguments passed through unchanged.
- For help requests (`--help`, `-h`, or `help`), delegates directly to `codex exec` and does not require profiles to be configured.
- Automatically selects among configured multicodex profiles.
- Re-checks file-backed auth isolation before launching `codex exec`.
- Prefers profiles whose five-hour usage window is strictly below 60%.
- Among eligible profiles, chooses the lowest weekly usage.
- When no profile is below the five-hour threshold, falls back to the lowest weekly-usage profile.
- When usage fetch is unavailable for every profile, falls back to the first sorted profile with `auth.json`, otherwise the first sorted configured profile.
- Returns child exit code.

`multicodex switch-global <name> [--force]`
- Explicit global operation.
- Changes only minimal auth pointer or file required for default Codex identity.
- Re-checks file-backed auth isolation before switching unless `--force` is supplied.
- Refreshes restore metadata whenever the current default auth state changed outside multicodex.
- Avoids touching unrelated Codex session data.

`multicodex status`
- Shows all profiles and each profile login status.
- Includes which profile is current global default when known.

`multicodex heartbeat`
- Runs a minimal read-only `codex exec --skip-git-repo-check --sandbox read-only --color never hello` keepalive for each logged-in profile.
- Skips profiles that are currently logged out.
- Re-checks file-backed auth isolation before per-profile Codex execution.
- Uses a non-blocking local lock so overlapping heartbeat runs are skipped instead of overlapping.
- Retries failed logged-in profile heartbeats with linear backoff by default.
- Prints per-profile result rows and a final summary.
- Returns non-zero when no logged-in profiles are found or when any logged-in profile heartbeat fails.
- Leaves the global default auth pointer untouched.
- Supports environment overrides for timeout, retries, backoff, prompt, and lock path.

`multicodex monitor`
- Runs a live terminal UI for Codex subscription usage across compatible local accounts.
- Defaults to the integrated monitor UI when no monitor subcommand is provided.
- Prefers account definitions from multicodex profile config and monitor-owned account overrides.
- Always includes the default Codex home as a candidate account home before broader filesystem discovery.
- Shows account labels instead of raw email addresses in the TUI when labels are available.
- Keeps tracked timestamps in UTC internally while rendering user-facing TUI timestamps in local time without seconds.
- Continues to support legacy monitor account-file locations as a compatibility fallback.
- Uses read-only filesystem auto-discovery under the home directory, scanning for `.codex*`, `.codex`, and `codex-home` paths up to depth 5 before filtering transient/cache locations and requiring usage signals.
- Treats observed-token totals as local estimates derived from session logs rather than official provider counters.
- Remains read-only with respect to Codex account state.

`multicodex monitor help`
- Prints monitor-specific usage, flags, and completion examples.

`multicodex monitor tui`
- Explicit alias for the integrated monitor terminal UI.
- Accepts the same flags and behavior contract as `multicodex monitor`.

`multicodex monitor doctor`
- Runs read-only monitor setup and source checks.
- Supports JSON output for automation.
- Checks codex binary access, auth-file readability, app-server usage fetch, and oauth usage fetch.
- Exits success when at least one usage source works, while surfacing degraded output when a source is unavailable.

`multicodex monitor completion`
- Compatibility alias for shell completion setup after migration from the standalone monitor.
- Defaults to bash when no shell is provided.
- Prints the full `multicodex` completion script.

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

`multicodex help [command [subcommand]]`
- Prints global help when no command topic is provided.
- Prints command-specific usage, description, and examples for one topic, including nested monitor topics such as `monitor doctor`, `monitor completion`, and `monitor tui`.

## Error handling
- Fail fast with actionable messages.
- Never dump secret content in errors.
- Use deterministic exit codes.

## Profile naming
- Profile names may include letters, numbers, `@`, `.`, `_`, and `-`.
- This allows account-like names such as `me@example.com`.
