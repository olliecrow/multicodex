# Command Specification

## Command Set

- `multicodex init`
- `multicodex add <name>`
- `multicodex login <name> [codex login args]`
- `multicodex login-all`
- `multicodex cli [--account <name>] [--] [codex args...]`
- `multicodex exec [--search] [codex exec args]`
- `multicodex generate [--search] [--account <name>] [-m|--model <model>] [--effort <effort>] [--base-instructions-file <path>] [--developer-instructions-file <path>] [--output-schema <path>] [--json] [prompt]`
- `multicodex status`
- `multicodex reconcile`
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
- With no `profile_resources` block, fills in missing portable top-level profile skill entries from the default Codex skills tree and leaves guidance untouched. Runtime-managed `.system` content stays profile-local.
- Leaves regular profile-local config, guidance, and skill overrides in place.

`multicodex login <name> [codex login args]`
- Runs official `codex login` in the selected profile context.
- Passes extra login args through to `codex login`.
- Requires the effective profile config to enable `cli_auth_credentials_store = "file"`.
- Normalizes regular profile `auth.json` permissions to `0600` after login.

`multicodex login-all`
- Runs profile-scoped login for each known profile in sorted order.
- Summarizes success and failure per profile.

`multicodex cli [--account <name>] [--] [codex args...]`
- Runs the interactive official Codex CLI with an automatically selected account by default.
- Without `--account`, uses the same configured-profile priority, weekly-reset, exhaustion, Spark, and protected default-reserve selection rules as `multicodex exec`.
- Automatic mode prepares and validates every configured profile before selection and fails closed if any configured profile is unsafe or incompatible.
- Parses explicit `--model`, `--model=`, `-m`, and `-m=` arguments for automatic routing.
- With a leading `--account <name>`, bypasses usage selection and launches that configured profile even when its weekly usage is exhausted or unavailable.
- Manual mode prepares and validates only the named profile, so unrelated profile errors do not block it.
- Removes one optional `--` immediately after the manual profile name; passes all other Codex arguments through unchanged.
- Treats `cli --help` and `cli -h` as multicodex help requests without selecting an account.
- Includes the existing default Codex home only as the final automatic reserve and verifies its login with the official Codex CLI before launch.
- Does not inject model, reasoning, sandbox, approval, or search defaults.
- Uses the selected home's Codex config defaults unless the caller passes explicit Codex args.
- Re-checks file-backed auth isolation before every configured-profile launch.
- Replaces the multicodex process with `codex` when stdin, stdout, and stderr are real terminals.
- Keeps auth, threads, sessions, and `/goal` state inside the selected `CODEX_HOME`.
- Never changes or manages the default Codex account authentication.

`multicodex exec [--search] [codex exec args]`
- Runs `codex exec` with all remaining arguments passed through unchanged.
- Moves `--search` before the `exec` subcommand because Codex defines it as a global flag.
- Treats `--search` after `--` as prompt text.
- Delegates exact help requests (`--help`, `-h`, or `help`) directly to `codex exec` without requiring profiles.
- Automatically selects among configured multicodex profiles first.
- Includes the default Codex home as a built-in reserve account after configured profiles.
- Can run with no configured profiles when the official Codex CLI confirms that the default account is logged in.
- Re-checks file-backed auth isolation before launching configured profiles.
- Parses model selection arguments (`--model`, `--model=`, and `-m`) for routing.
- If the model contains `spark` case-insensitively, selects Spark weekly usage when available.
- If Spark is requested, configured profiles need Spark usage data to win normal routing.
- Excludes configured profiles whose requested weekly bucket is known to be exhausted.
- Orders candidates by configured selection priority, then known weekly reset soonest, then unknown weekly reset.
- Randomizes only exact reset ties or equally unknown reset times.
- Uses the default Codex home only when no configured profile has usable weekly usage.
- Before launching the default reserve, runs a bounded `codex login status` in the default Codex home so file and OS keyring credential stores are both supported.
- If the default Codex home is the only remaining destination, uses it as the final fallback even when its usage data is unavailable or exhausted, provided its login is confirmed.
- If the default is logged out or its login status cannot be confirmed, returns a safe error without launching `codex exec`.
- Writes selected-profile metadata only under `MULTICODEX_HOME/run` when `MULTICODEX_SELECTED_PROFILE_PATH` is set.
- Selected-profile metadata exposes the optional usage field `weekly_used_percent`; the older generic percent fields are not emitted.
- Returns the child exit code.

`multicodex generate [--search] [--account <name>] [-m|--model <model>] [--effort <effort>] [--base-instructions-file <path>] [--developer-instructions-file <path>] [--output-schema <path>] [--json] [prompt]`
- Sends one text prompt through Codex App Server using ChatGPT subscription authentication and Codex's built-in OpenAI provider and endpoint.
- Reads the prompt from standard input when no prompt argument is present and rejects input larger than 4 MiB.
- Uses normal weekly-aware routing, including the protected default reserve, unless `--account <name>` selects one configured profile directly.
- Passes an explicit model to the existing model-aware selector. Otherwise, uses the highest-priority visible model in the installed Codex bundled catalog.
- Validates `--effort` against the selected model's bundled supported-effort metadata before generation. Without the flag, uses the selected model's bundled default effort.
- Supports exactly `codex-cli 0.147.0` and `0.148.0`, and fails before generation for any other version.
- Requires App Server `account/read` to report managed ChatGPT authentication. It rejects API-key billing and does not start login or token-refresh flows.
- Runs an ephemeral thread in a private empty temporary directory with read-only sandboxing and approval policy `never`.
- Sends exact base or developer instruction file contents when selected and empty client instructions otherwise. The prompt and each optional input file have independent 4 MiB limits; file inputs must resolve to regular files.
- Accepts `--output-schema` only when the file contains a JSON object and passes that object to App Server for structured-output enforcement.
- Disables client context sources, coding tools, MCP servers, and notification hooks, ignores unrelated custom model providers, and uses a private `0600` one-model catalog with agent-tool metadata removed.
- Exposes no tools by default. `--search` enables only the native live web-search tool; all other tools remain disabled.
- Fails closed if Codex config replaces the built-in OpenAI provider, overrides its endpoint, changes the selected web-search mode, loads a configuration lockfile, or defines any non-null nested `tools.web_search` settings while `--search` is selected, such as domain, search-context, or approximate-location filters.
- Rejects server requests, command, file, image, unexpected tool events, and web-search events unless `--search` was selected.
- By default, streams only assistant text to standard output. Resource notices and safe errors use standard error. A failure can occur after partial text was written.
- With `--json`, buffers up to 16 MiB of assistant text and writes one object only after success. The object contains response text, model, effective effort, elapsed milliseconds, and numeric token usage. Usage is `null` when App Server emits no usage event. It excludes account and profile identifiers, paths, reasoning text, and raw events. A generation failure or response-limit failure writes no JSON object.
- Writes selected-profile metadata under `MULTICODEX_HOME/run` when `MULTICODEX_SELECTED_PROFILE_PATH` is set. The selection source is `explicit_account`, `usage_selector`, or `usage_selector_default_reserve`.
- Deletes the temporary workspace and model catalog when the command ends.
- Does not expose sessions, custom tools, images, raw events, or a persistent App Server.
- Re-checks profile filesystem and file-backed auth isolation before a configured-profile run. It never changes or manages default account authentication.

`multicodex status`
- Shows all profiles and each profile login status.
- Does not manage or inspect the default Codex account as a multicodex profile.
- Remains auth-only: it does not validate, reconcile, or claim readiness for configured profile resources.

`multicodex reconcile`
- Reconciles managed setup, guidance, and skill resources for every registered profile in sorted order.
- Uses the same profile path, permission, config, and no-clobber rules as profile-scoped commands.
- May create missing profile directories, repair multicodex-generated `config.toml` state, and create, retarget, or remove multicodex-owned resource links.
- Preserves regular profile guidance, config, auth, and skill overrides.
- Does not inspect auth, launch Codex, create Codex sessions, or change the default Codex home.
- Continues through independent profile failures, reports every failure, and exits non-zero if any profile fails.

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
- By default, builds account candidates from the global Codex home, monitor-owned account overrides, and configured multicodex profiles.
- Labels the global Codex home `global` and accepts `--include-default=false` to omit it for one run.
- Supports opt-in account sources with `--include-active` and `--discover`.
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
- Reports successful source checks as `plan=<plan> weekly=<used>% source=<source>`, using `weekly=unavailable` when the provider supplies no weekly window.
- Checks the global Codex home, configured monitor accounts, and configured multicodex profiles by default.
- Uses the normal source policy by default: app-server first for validated profile homes, direct OAuth for other homes.
- Accepts `--include-default=false` to omit the global Codex home, and adds active `CODEX_HOME`, filesystem discovery, or raw app-server checks only when the caller passes `--include-active`, `--discover`, or `--app-server`.
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
- Skills use ordered source directories with first-source-wins merging. Runtime-managed `.system` content is excluded, and regular top-level profile entries override inherited entries.
- Explicit resource management owns symlinks at its managed profile positions, including pre-existing symlinks. It may retarget or remove them and reports old targets. Regular files and directories are preserved.
- `inherit: false` removes managed symlinks. Populated source fields are invalid in this mode.
- `~` expands to the user home. Relative paths resolve from the config file directory. Custom source directories must exist and have the expected type before reconciliation starts.
- `add`, `login`, `login-all`, `cli`, `exec`, `generate`, and `heartbeat` reconcile resources before a profile-scoped Codex launch. `reconcile` applies the same managed profile state to all profiles without launching Codex. `doctor`, `dry-run`, `status`, and `monitor` do not mutate profile resources.
- Resource changes use normal command output, except `exec` and `generate` write them to standard error so standard output remains safe for scripts.

`multicodex completion <shell>`
- Prints tab-completion script for bash, zsh, or fish.
- Supports command name completion and dynamic profile-name completion.

`multicodex help [command [subcommand]]`
- Prints global help when no topic is provided.
- Prints command-specific usage, description, and examples for one topic, including nested monitor topics.

`multicodex version`
- Prints the build version on one line. Tagged release binaries report their tag; untagged source builds report a development version.

## Error Handling

- Fail fast with actionable messages.
- Reject undocumented positional arguments with exit code `2` instead of silently ignoring them.
- Never dump secret content in errors.
- External failures report safe status or exit codes and allowlisted recovery guidance, not raw provider bodies, app-server messages, or subprocess failure output.
- Use deterministic exit codes where practical.

## Profile Naming

- Profile names may include letters, numbers, `@`, `.`, `_`, and `-`.
- Account-like names are allowed, but docs and tests should prefer non-personal labels such as `personal` and `work`.
