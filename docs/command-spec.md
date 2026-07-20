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
- `multicodex dry-run [operation]`
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
- Applies the configured profile resource policy before saving the profile.
- With no `profile_resources` block, fills in missing top-level profile skill entries from the default Codex skills tree and leaves guidance untouched.
- Leaves regular profile-local config, guidance, and skill overrides in place.

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
- Automatically selects among configured multicodex profiles first.
- Includes the default Codex home as a built-in reserve account after configured profiles.
- Can run with no configured profiles by using the default Codex home as the only available account.
- Re-checks file-backed auth isolation before launching configured profiles.
- Parses model selection arguments (`--model`, `--model=`, and `-m`) for routing.
- If the model contains `spark` case-insensitively, selects Spark weekly usage when available.
- If Spark is requested, configured profiles need Spark usage data to win normal routing.
- Excludes configured profiles whose requested weekly bucket is known to be exhausted.
- Orders candidates by configured selection priority, then known weekly reset soonest, then unknown weekly reset.
- Randomizes only exact reset ties or equally unknown reset times.
- Uses the default Codex home only when no configured profile has usable weekly usage.
- If the default Codex home is the only remaining destination, uses it as the final fallback even when its usage data is unavailable or exhausted.
- Returns a usage-selection error only when no configured profile is usable and the default Codex home is not available as a reserve candidate.
- Writes selected-profile metadata only under `MULTICODEX_HOME/run` when `MULTICODEX_SELECTED_PROFILE_PATH` is set.
- Selected-profile metadata exposes the optional usage field `weekly_used_percent`; the older generic percent fields are not emitted.
- Returns the child exit code.

`multicodex status`
- Shows all profiles and each profile login status.
- Does not manage or inspect the default Codex account as a multicodex profile.
- Remains auth-only: it does not validate, reconcile, or claim readiness for configured profile resources.

`multicodex heartbeat`
- Runs `codex exec --skip-git-repo-check --ephemeral --sandbox read-only --color never hello` for each logged-in profile.
- Does not persist Codex session files.
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
- Uses Codex app-server usage fetches for validated multicodex profile homes, with direct OAuth as fallback.
- Uses direct OAuth for other monitor account homes unless they dedupe with a validated profile home.
- Extracts official weekly windows by their declared 10,080-minute duration, with a narrow older-response fallback that treats an undeclared secondary window as weekly.
- Remains read-only with respect to Codex account state.
- Renders one full-width weekly card per account.
- Shows default and Spark weekly usage on separate lines when Spark data is present.
- Shows a restrained progress bar where it fits, the reset countdown, and the exact local reset time where useful.
- Shows configured labels before raw identity fields.
- Orders account rows by weekly reset time.
- Keeps timestamps in UTC internally and renders user-facing timestamps in local time.
- Treats the seven-day observed-token total as a local estimate from session logs.
- Keeps last good official window cards visible and marked stale during full refresh outages.

`multicodex monitor tui`
- Explicit alias for the integrated monitor terminal UI.
- Accepts the same flags and behavior contract as `multicodex monitor`.

`multicodex monitor doctor`
- Runs read-only monitor setup and source checks.
- Supports JSON output.
- Checks configured monitor accounts and configured multicodex profiles by default.
- Uses the normal source policy by default: app-server first for validated profile homes, direct OAuth for other homes.
- Adds default Codex home, active `CODEX_HOME`, filesystem discovery, or raw app-server checks only when the caller passes `--include-default`, `--include-active`, `--discover`, or `--app-server`.
- Exits success when at least one usage fetch works and fails when no usage fetch works.
- Reports degraded status when at least one usage fetch works but another usage fetch or setup check fails.

`multicodex monitor completion`
- Defaults to bash when no shell is provided.
- Prints the full `multicodex` completion script.

`multicodex doctor`
- Runs non-mutating setup and auth checks.
- Reports `ok`, `warn`, and `fail` checks with a final pass or fail summary.
- Supports JSON output.
- Includes repository leak-guard checks for auth homes in git worktrees, recommended ignore patterns, and tracked sensitive-looking files.
- Resolves and validates configured profile resource sources without reconciling profile files.

`multicodex dry-run`
- Prints planned operations without executing commands or mutating files.
- Supports an operation-specific preview for `login <name>`.
- Resolves configured profile resource paths and shows the effective policy and planned reconciliation.

## Profile Resource Reconciliation

- `profile_resources` is an optional version-1 config block. Its omission preserves the established guidance no-op and strict default-skill reconciliation exactly.
- Present `guidance` and `skills` objects require a boolean `inherit`. Unknown keys inside the resource block are errors; unrelated top-level config keys remain permissive.
- Guidance uses one source directory and manages only `AGENTS.md` and `AGENTS.override.md`. Either regular profile file overrides the whole inherited pair.
- Skills use ordered source directories with first-source-wins merging. Regular top-level profile entries override inherited entries.
- Explicit resource management owns symlinks at its managed profile positions, including pre-existing symlinks. It may retarget or remove them and reports old targets. Regular files and directories are preserved.
- `inherit: false` removes managed symlinks. Populated source fields are invalid in this mode.
- `~` expands to the user home. Relative paths resolve from the config file directory. Custom source directories must exist and have the expected type before reconciliation starts.
- `add`, `login`, `login-all`, `cli`, `exec`, and `heartbeat` reconcile resources before a profile-scoped Codex launch. `doctor`, `dry-run`, `status`, and `monitor` do not mutate profile resources.

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
