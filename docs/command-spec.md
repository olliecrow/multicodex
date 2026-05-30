# Command Specification

## Command Set

- `multicodex init`
- `multicodex add <name>`
- `multicodex login <name> [codex login args]`
- `multicodex login-all`
- `multicodex cli <name> [codex args...]`
- `multicodex exec [codex exec args]`
- `multicodex status`
- `multicodex heartbeat`
- `multicodex monitor [flags]`
- `multicodex monitor tui [flags]`
- `multicodex monitor doctor [flags]`
- `multicodex monitor completion [shell]`
- `multicodex doctor [--json] [--timeout 8s]`
- `multicodex dry-run [login]`
- `multicodex completion <bash|zsh|fish>`
- `multicodex help [command [subcommand]]`
- `multicodex version` and `multicodex --version`

Multicodex intentionally has no command for changing the shared default Codex account.

## Behavior Contract

`multicodex init`
- Creates local multicodex metadata only.
- Uses `MULTICODEX_HOME` when set, otherwise defaults to `~/multicodex`.
- Does not modify existing default Codex auth.

`multicodex add <name>`
- Registers a named profile.
- Creates a profile-local `CODEX_HOME` with private permissions.
- Defaults profile `config.toml` to a symlink to the default Codex `config.toml`.
- Fills in missing top-level profile skill entries with symlinks to the default Codex skills tree.
- Leaves manual profile-local config files and manual top-level skill overrides in place.

`multicodex login <name> [codex login args]`
- Runs official `codex login` in the selected profile context.
- Passes extra login args through to `codex login`.
- Requires the effective profile config to enable `cli_auth_credentials_store = "file"`.
- Normalizes regular profile `auth.json` permissions to `0600` after login.

`multicodex login-all`
- Runs profile-scoped login for each known profile in sorted order.
- Summarizes success and failure per profile.

`multicodex cli <name> [codex args...]`
- Runs the interactive official Codex CLI with the selected profile's `CODEX_HOME`.
- Does not inject model, reasoning, sandbox, approval, or search defaults.
- Uses shared Codex config defaults unless the caller passes explicit Codex args.
- Re-checks file-backed auth isolation before launch.
- Replaces the multicodex process with `codex` when stdin, stdout, and stderr are real terminals.
- Keeps auth, threads, sessions, and `/goal` state profile-local.
- Leaves the default Codex account untouched.

`multicodex exec [codex exec args]`
- Runs `codex exec` with all remaining arguments passed through unchanged.
- Delegates exact help requests (`--help`, `-h`, or `help`) directly to `codex exec` without requiring profiles.
- Automatically selects only among configured multicodex profiles.
- Re-checks file-backed auth isolation before launch.
- Parses model selection arguments (`--model`, `--model=`, and `-m`) for routing.
- If the model contains `spark` case-insensitively, selects Spark usage windows when available.
- If Spark is requested but Spark usage data is unavailable, returns the usage-selection error instead of falling back to default-window routing.
- Treats profiles whose five-hour usage window is strictly below 40% as eligible.
- Excludes profiles whose weekly window is known to be exhausted.
- Among eligible profiles, picks the one whose weekly reset is soonest.
- When usage fetch is unavailable for every profile, falls back to the first configured profile after profile safety checks pass.
- Writes selected-profile metadata only under `MULTICODEX_HOME` when `MULTICODEX_SELECTED_PROFILE_PATH` is set.
- Returns the child exit code.

`multicodex status`
- Shows all profiles and each profile login status.
- Does not manage or inspect the default Codex account as a multicodex profile.

`multicodex heartbeat`
- Runs `codex exec --skip-git-repo-check --sandbox read-only --color never hello` for each logged-in profile.
- Skips logged-out profiles.
- Re-checks file-backed auth isolation before per-profile execution.
- Uses a non-blocking lock under `MULTICODEX_HOME`.
- Retries failed logged-in profile heartbeats with linear backoff by default.
- Prints per-profile result rows and a final summary.
- Returns non-zero when no logged-in profiles are found or any logged-in profile heartbeat fails.
- Leaves the default Codex account untouched.
- Supports environment overrides for timeout, retries, backoff, and lock path.
- Rejects lock paths that resolve outside `MULTICODEX_HOME`.

`multicodex monitor`
- Runs a live terminal UI for Codex subscription usage.
- Defaults to the integrated monitor UI when no monitor subcommand is provided.
- Defaults both poll interval and per-poll fetch timeout to 60 seconds.
- By default, builds account candidates only from monitor-owned account overrides and configured multicodex profiles.
- Supports opt-in account sources with `--include-default`, `--include-active`, and `--discover`.
- Uses OAuth usage fetches for normal account rows.
- Remains read-only with respect to Codex account state.
- Renders compact usage lines in each window card.
- Shows Spark usage inline when Spark data is present.
- Shows configured labels before raw identity fields.
- Orders account rows by weekly reset time.
- Keeps timestamps in UTC internally and renders user-facing timestamps in local time.
- Treats observed-token totals as local estimates from session logs.
- Keeps last good official window cards visible and marked stale during full refresh outages.

`multicodex monitor tui`
- Explicit alias for the integrated monitor terminal UI.
- Accepts the same flags and behavior contract as `multicodex monitor`.

`multicodex monitor doctor`
- Runs read-only monitor setup and source checks.
- Supports JSON output.
- Checks configured monitor accounts and configured multicodex profiles by default.
- Uses OAuth fetch checks by default.
- Adds default Codex home, active `CODEX_HOME`, filesystem discovery, or app-server checks only when the caller passes `--include-default`, `--include-active`, `--discover`, or `--app-server`.
- Exits success when at least one usage fetch works and fails when no usage fetch works.
- Reports degraded status when some usage fetches fail but at least one works.

`multicodex monitor completion`
- Defaults to bash when no shell is provided.
- Prints the full `multicodex` completion script.

`multicodex doctor`
- Runs non-mutating setup and auth checks.
- Reports `ok`, `warn`, and `fail` checks with a final pass or fail summary.
- Supports JSON output.
- Includes repository leak-guard checks for auth homes in git worktrees, recommended ignore patterns, and tracked sensitive-looking files.

`multicodex dry-run`
- Prints planned operations without executing commands or mutating files.
- Supports an operation-specific preview for `login <name>`.

`multicodex completion <shell>`
- Prints tab-completion script for bash, zsh, or fish.
- Supports command name completion and dynamic profile-name completion.

`multicodex help [command [subcommand]]`
- Prints global help when no topic is provided.
- Prints command-specific usage, description, and examples for one topic, including nested monitor topics.

## Error Handling

- Fail fast with actionable messages.
- Never dump secret content in errors.
- Use deterministic exit codes where practical.

## Profile Naming

- Profile names may include letters, numbers, `@`, `.`, `_`, and `-`.
- Account-like names are allowed, but docs and tests should prefer non-personal labels such as `personal` and `work`.
