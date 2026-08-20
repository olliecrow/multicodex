# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine without changing the normal default Codex account.

It keeps non-default accounts in named local profiles. Each profile has its own `CODEX_HOME`, auth file, sessions, threads, and Codex state. The regular system Codex account remains managed by Codex itself, outside multicodex.

By default, each profile reuses your global Codex `config.toml` through a symlink, so model defaults, reasoning settings, permission settings, and other normal Codex config changes apply everywhere. Profile homes also inherit missing top-level skill entries from the global Codex skills directory through symlinks.

Profile login requires file-backed auth. If the effective Codex config does not set `cli_auth_credentials_store = "file"`, profile login and profile-scoped Codex execution fail with a setup error instead of sharing global auth state.

## Status

- Usable for local multi-account Codex CLI, `codex exec`, harness-free generation, heartbeat, and usage-monitor workflows.
- The command surface is intentionally narrow. Multicodex does not implement global account switching.

## Prerequisites

- Go 1.25 or newer for building from source.
- Development and CI checks use the patched Go toolchain listed in `go.mod`.
- Official `codex` CLI installed and available in `PATH`.
- Git for `multicodex editor` project and worktree checks.
- tmux 3.2 or newer for `multicodex editor`.
- macOS or Linux.

## Install

Build from source.

```bash
go build -o multicodex ./cmd/multicodex
```

Or install from the public module path.

```bash
go install github.com/olliecrow/multicodex/cmd/multicodex@latest
```

Optional local install path.

```bash
mv ./multicodex ~/.local/bin/multicodex
```

Tagged releases publish checksummed macOS and Linux archives for AMD64 and ARM64. Release binaries report their tag through `multicodex version`; untagged source builds report a development version. Verify an archive against `SHA256SUMS` before installing it.

Contributor setup, required checks, and the release process are documented in [CONTRIBUTING.md](CONTRIBUTING.md).

## Quick Start

```bash
multicodex init
multicodex add personal
multicodex add work
multicodex login personal
multicodex login work
multicodex status
multicodex reconcile
```

Run interactive Codex on the best available account, or select one profile directly.

```bash
multicodex cli
multicodex cli --account work -- "check this repo"
```

Run `codex exec` on the best available account.

```bash
multicodex exec -s read-only "Summarize the README in 3 bullets."
```

Generate text through a subscription account without the client-side coding-agent instructions or tools.

```bash
multicodex generate "Write a short product description."
printf '%s' "Summarize this text." | multicodex generate
```

Open the monitor and run checks.

```bash
multicodex monitor
multicodex editor
multicodex doctor
multicodex monitor doctor
multicodex dry-run
```

## Local State

- Default multicodex state home is `~/multicodex`.
- Override the state location with `MULTICODEX_HOME`.
- Profile auth stays under `~/multicodex/profiles/<name>/codex-home/auth.json`.
- Profile sessions, threads, and `/goal` state stay under that profile's `codex-home`.
- Multicodex state directories, profile directories, profile `codex-home`, profile skills directories, `auth.json`, selected-profile metadata under `MULTICODEX_HOME/run`, heartbeat lock files, and config lock files must be profile-local regular filesystem entries with local-user-only directory permissions. Symlinks and hard links are rejected where they could cross account boundaries.
- Profile `config.toml` defaults to a symlink from `~/multicodex/profiles/<name>/codex-home/config.toml` to the default Codex config at `~/.codex/config.toml`.
- Unless configured otherwise, profile skills fill in missing portable top-level entries from `~/.codex/skills` using symlinks. Runtime-managed `.system` content is excluded, and manual top-level profile skill overrides are left in place.
- To use a per-profile Codex config, replace the profile `config.toml` symlink with a regular profile-local `config.toml` file that still enables file-backed auth.

## Configurable Profile Resources

The optional `profile_resources` block in `~/multicodex/config.json` controls shared guidance and skill links for every profile. If the block is omitted, behavior is unchanged: multicodex does not touch `AGENTS.md` or `AGENTS.override.md`, and skills keep inheriting from the default Codex home.

```json
{
  "version": 1,
  "profile_resources": {
    "guidance": {
      "inherit": true,
      "source": "~/.codex"
    },
    "skills": {
      "inherit": true,
      "sources": ["~/.codex/skills", "shared-skills"]
    }
  },
  "profiles": {}
}
```

- `guidance.inherit: true` links the source directory's `AGENTS.md` and `AGENTS.override.md`. An omitted or empty `source` uses the default Codex home.
- `skills.inherit: true` merges portable top-level entries from ordered `sources`; the first source wins name conflicts. Runtime-managed `.system` content is excluded. An omitted `sources` key uses the default Codex skills directory. An explicit empty list is invalid.
- `inherit: false` removes symlinks managed at that resource's profile locations. It never removes regular files or directories.
- Either regular profile guidance file makes both guidance names a local override. Regular top-level profile skill entries override inherited entries with the same name.
- `~` expands to the user home. Relative paths resolve from the directory containing `config.json`, normally `~/multicodex`, not from the current working directory.
- Custom source directories must exist. Resource blocks require a correctly spelled boolean `inherit` and reject unknown nested keys.
- When explicit management is enabled, symlinks at the two guidance names and directly under the profile `skills/` directory are multicodex-owned. Retargeting or removal reports the old target.
- Codex's existing user-wide `$HOME/.agents/skills` and repository `.agents/skills` discovery stays separate and continues to work normally.

Use `multicodex doctor` to validate configured sources and `multicodex dry-run` to see the effective policy and planned reconciliation without changing files. To recover from a bad link policy, set the affected `inherit` value to `false` and remove its `source` or `sources` field, run `multicodex reconcile`, then remove the optional block to return to the original unmanaged-guidance and default-skill behavior.

Run `multicodex reconcile` to apply the configured guidance and skill policy to every registered profile without inspecting auth or launching Codex. It uses the same no-clobber rules as `add`, `login`, `cli`, `exec`, and `heartbeat`: regular profile guidance and skill entries remain local overrides, while multicodex-owned links are created, retargeted, or removed as needed.

## Commands

```text
multicodex init
multicodex add <name>
multicodex login <name> [codex login args]
multicodex login-all
multicodex cli [--account <name>] [--] [codex args...]
multicodex exec [--search] [codex exec args]
multicodex generate [--search] [--account <name>] [-m|--model <model>] [--effort <effort>] [--base-instructions-file <path>] [--developer-instructions-file <path>] [--output-schema <path>] [--json] [prompt]
multicodex status
multicodex reconcile
multicodex heartbeat
multicodex monitor [flags]
multicodex monitor tui [flags]
multicodex monitor doctor [flags]
multicodex monitor completion [shell]
multicodex editor
multicodex doctor [--json] [--timeout 8s]
multicodex dry-run [operation]
multicodex completion <bash|zsh|fish>
multicodex version
multicodex help [command [subcommand]]
multicodex --version
```

Commands reject undocumented positional arguments instead of silently ignoring them.

## Interactive CLI

`multicodex cli [codex args...]` launches the official interactive `codex` CLI after applying the same weekly-usage account selection as `multicodex exec`. Add `--account <name>` before the Codex arguments to bypass routing and use one configured profile. An optional `--` after the profile name separates multicodex arguments from Codex arguments and is not passed through.

Automatic mode prepares and validates every configured profile, considers profiles before the protected default reserve, and uses explicit `-m` or `--model` arguments for Spark routing. Manual mode prepares and validates only the named profile and does not inspect usage. Codex defaults such as model, reasoning level, approvals, sandbox, and search come from the selected home unless you pass explicit Codex args. Multicodex does not inject its own model or permission defaults.

Two terminals can run `multicodex cli --account <name>` with different profiles at the same time. Each terminal uses its own account, auth, threads, and `/goal` state because each one has a different `CODEX_HOME`.

## Automatic Routing

`multicodex cli`, `multicodex exec`, and `multicodex generate` select among configured multicodex profiles, with the default Codex home as a built-in reserve account. Manual `cli --account <name>` and `generate --account <name>` launches do not use these rules.

- Help requests such as `multicodex exec --help` delegate directly to `codex exec` and do not require profiles.
- `multicodex exec --search ...` enables Codex live web search by placing the global Codex flag before the `exec` subcommand. A `--search` token after `--` remains prompt text.
- Automatic routing can run with no configured profiles when Codex confirms that the default account is logged in.
- Configured profiles at 100% weekly usage are not selected.
- Automatic routing uses configured selection priority first, then prefers the profile whose known weekly reset is soonest.
- Profiles with an unknown weekly reset follow profiles with a known reset. Exact ties are randomized.
- The default Codex home is a protected reserve. Automatic `cli` and `exec` routing use it only when no configured profile has usable weekly usage.
- Before launching the default reserve, multicodex asks the official Codex CLI to confirm its login. File and OS keyring credential stores are both supported; an absent `auth.json` does not imply that the default is logged out.
- If the default Codex home is the only remaining destination, automatic routing uses it as the final fallback even when its usage data is unavailable or exhausted, provided its login is confirmed.
- For explicit Spark model names, configured profiles need Spark usage windows to win normal routing; the logged-in default Codex home still remains the final fallback.
- If the default is logged out or its login status cannot be confirmed, automatic routing fails without launching the prompt.

## Harness-Free Generation

`multicodex generate` sends one text prompt through Codex App Server and the selected ChatGPT subscription. It always uses Codex's built-in OpenAI provider and default provider endpoint. By default, it streams assistant text to standard output. If the prompt argument is omitted, it reads up to 4 MiB from standard input.

```bash
multicodex generate "Draft a concise release note."
multicodex generate --account work "Translate this paragraph to French."
multicodex generate -m gpt-5.5 < prompt.txt
multicodex generate --effort high --developer-instructions-file instructions.md "Analyze this scenario."
multicodex generate --search --base-instructions-file instructions.md "Research the latest release."
multicodex generate --output-schema schema.json --json "Return the requested fields."
```

Use `--base-instructions-file` and `--developer-instructions-file` to supply exact instruction text. Each instruction file, the prompt, and the optional `--output-schema` file has a 4 MiB limit. File inputs must resolve to regular files. The schema file must contain one JSON object and is passed to App Server for structured-output enforcement. Use `--effort` to select an effort supported by the chosen model; multicodex validates it against the installed bundled model catalog before generation. Without this flag, the model's catalog default applies.

By default, generation has no tools. Use `--search` to expose only Codex's native live web-search tool. Shell, file, image, MCP, app, plugin, computer-use, and coding-agent tools remain disabled.

Use `--json` for one atomic JSON object instead of streamed text. It contains `text`, `model`, `effort`, `duration_ms`, `web_search_calls`, and `usage`. `web_search_calls` counts distinct native search items by their nonblank lifecycle IDs of at most 256 bytes and is zero when no native search ran. Generation fails if search activity has no lifecycle item, an item has an invalid ID, or a turn exceeds 1,024 distinct search IDs. The numeric count adds no search query, URL, result, account, profile, path, raw event, or reasoning data; assistant `text` can contain model-written citations or search-derived content. Usage contains only numeric token counts and is `null` if App Server emits no usage event. If generation fails or the buffered response exceeds 16 MiB, JSON mode writes no response object.

The request uses an ephemeral thread in a private empty directory. Multicodex supplies empty base and developer instructions unless their files are given, disables project context, coding tools, MCP servers, and notification hooks, ignores unrelated custom model providers, and limits the temporary model catalog to one model with agent-tool metadata removed. It fails closed if Codex config replaces the built-in OpenAI provider or its endpoint, changes the requested web-search mode, loads a configuration lockfile, or defines any non-null nested `tools.web_search` settings for an opted-in search, such as domain, search-context, or approximate-location filters. It rejects API-key authentication, server action requests, and every tool or item outside the optional native web-search boundary. This removes the client-side Codex coding-agent harness; it cannot control provider-side instructions.

The command supports `codex-cli 0.147.0` and `0.148.0`. App Server and its model catalog are experimental Codex interfaces, so multicodex fails closed on another version until compatibility is tested. The default model is the highest-priority visible model in Codex's bundled catalog. Use `--model` to select another bundled model.

The command remains one-shot. It does not expose sessions, custom tools, images, raw events, or a daemon. Existing `cli` and `exec` commands remain unchanged for agent workflows.

## Heartbeat

`multicodex heartbeat` sends a minimal ephemeral, read-only keepalive hello to every logged-in profile. Heartbeat requests do not persist Codex session files.

```bash
multicodex heartbeat
```

Heartbeat:

- skips logged-out profiles
- uses a non-blocking lock under `MULTICODEX_HOME`
- retries failed profile heartbeats once by default
- runs profile-scoped `codex exec --skip-git-repo-check --ephemeral --sandbox read-only --color never hello`
- redacts raw failure output

Optional environment overrides:

- `MULTICODEX_HEARTBEAT_TIMEOUT_SECONDS`
- `MULTICODEX_HEARTBEAT_RETRIES`
- `MULTICODEX_HEARTBEAT_BACKOFF_SECONDS`
- `MULTICODEX_HEARTBEAT_LOCK_PATH`

`MULTICODEX_HEARTBEAT_LOCK_PATH` must resolve under `MULTICODEX_HOME`.

## Monitor

`multicodex monitor` shows live subscription usage across configured profile homes.

```bash
multicodex monitor
multicodex monitor tui
multicodex monitor --interval 30s
multicodex monitor doctor
multicodex monitor completion
```

By default, monitor account candidates come from:

- the global Codex home (normally `~/.codex`), labeled `global`
- explicit account file under `~/multicodex/monitor/accounts.json`
- configured multicodex profiles from `~/multicodex/config.json`

Additional sources are opt-in:

- `--include-active` includes the active `CODEX_HOME`
- `--discover` scans compatible Codex homes from the local filesystem
- `multicodex monitor doctor --app-server` also checks the raw Codex app-server source separately

Pass `--include-default=false` to omit the global Codex home for one run. Explicit account-file labels and configured profile labels take priority when they point to the same home, so duplicate cards are not shown.

For validated multicodex profile homes, the monitor asks the Codex app-server for usage first and falls back to direct OAuth from the profile home. This matches Codex CLI auth handling for logged-in profiles whose access token can still be refreshed. Other monitor account homes use direct OAuth unless they dedupe with a validated profile home.

Successful `multicodex monitor doctor` source checks report `plan=<plan> weekly=<used>% source=<source>`. When the provider supplies no weekly window, doctor reports `weekly=unavailable` instead of exposing an internal numeric marker.

The TUI:

- orders account rows by weekly reset time
- shows configured account labels before raw identity fields
- keeps timestamps in UTC internally and renders local time in the UI
- shows one full-width weekly card per account, with default and Spark usage on separate lines when available
- shows a compact progress bar where space permits, plus the reset countdown and exact local reset time
- shows one local seven-day observed-token estimate from session logs
- uses each official window's declared duration, with a narrow positional fallback for older provider responses
- keeps last good official window cards visible and marked stale during a full refresh outage
- prefers short re-login warnings when a profile token has expired

Example manual monitor account file:

```json
{
  "version": 1,
  "accounts": [
    {"label": "personal", "codex_home": "/path/to/personal/codex-home"},
    {"label": "work", "codex_home": "/path/to/work/codex-home"}
  ]
}
```

## Editor

`multicodex editor` is one terminal workspace for local and SSH projects. It shows a project and workspace sidebar, one active terminal, and compact local multicodex account usage. Projects move to the top when the last 100 rows of one of their terminals change.

Start it outside tmux:

```bash
multicodex editor
```

Press `Ctrl+G` for editor controls and `?` for shortcuts. From editor controls, press `g` to send a literal `Ctrl+G` to the terminal. Use `h` to add an existing system SSH alias, `p` to add an absolute project directory, `w` to create a named workspace, and `n` to create a shell or Codex window. Use `Alt+1` through `Alt+9` to select windows in current sidebar order. `Cmd+1` through `Cmd+9` also work when the terminal sends those keys to applications. Normal terminal paste uses the terminal application's paste action. Use `i` to copy a local clipboard image to the selected workspace, or `a` to attach a local file. The editor pastes the host-local path into the terminal draft and never submits it.

Each window is one single-pane tmux session on its project host. The outer editor never runs in tmux, and its managed tmux server disables the prefix keys and bindings. Detaching, sleep, SSH loss, or an editor restart leaves owned sessions running and reconnects the selected window. Other windows stay live and are polled through one warm host connection. `s` opens tmux copy mode with 50,000 lines of history.

For a Git project, every workspace is a new owned worktree and a new `multicodex/<name>-<id>` branch. Creation fetches the remote default base branch when available but never changes the source checkout or its base branch. A non-Git project has one in-place workspace and is never deleted as a directory. A workspace stays in the sidebar without a window. Configured projects with no workspace stay hidden but remain available from the new-workspace form.

Remote hosts must already work as system SSH aliases in non-interactive batch mode. They need tmux, Git, and the same multicodex release or clean source revision in `PATH`. Modified development builds cannot connect remotely because their exact source identity cannot be verified. Bounded SSH keepalives detect a dead path and trigger reconnection after sleep or network loss. Each host uses its own multicodex profiles and Codex sign-ins. The editor never copies auth state between hosts.

The client stores only hosts, projects, selection, and activity hashes under `MULTICODEX_HOME/editor`. Each host stores a private ownership registry, worktrees, and uploaded attachments under its own `MULTICODEX_HOME/editor`. Terminal output is never written to editor state. Startup and hourly cleanup remove only exact, inactive editor-owned resources older than seven days. Live terminals, dirty worktrees, branches with unique commits, uncertain resources, and unrelated tmux sessions or directories are preserved. Manual deletion uses the same checks and asks for confirmation before forced loss. A force confirmation applies only to that invocation and is never replayed after interruption.

## Checks And Completion

Run non-mutating checks and previews.

```bash
multicodex doctor
multicodex dry-run
multicodex dry-run login personal
```

Enable tab completion.

```bash
eval "$(multicodex completion zsh)"
eval "$(multicodex completion bash)"
multicodex completion fish > ~/.config/fish/completions/multicodex.fish
```

Get detailed help.

```bash
multicodex help
multicodex help cli
multicodex help exec
multicodex help heartbeat
multicodex help monitor
multicodex help monitor doctor
multicodex help editor
```

## Development Checks

```bash
go test ./...
go test -race ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.4.0 ./...
go build -o multicodex ./cmd/multicodex
```

## Safety Model

- Uses official `codex login` flows.
- Keeps profile auth and Codex state local to each profile `CODEX_HOME`.
- Does not store raw secrets in multicodex config.
- Does not change, restore, back up, symlink, or otherwise manage the shared default Codex auth account.
- Scrubs inherited account-routing and account-token environment variables before launching profile-scoped Codex commands.
- Reports external failures with safe status codes and recovery guidance without echoing raw provider bodies, app-server messages, or subprocess failure output.
- `monitor` is read-only and does not mutate Codex account data.
- `doctor` and `dry-run` are non-mutating helpers.
- `doctor` includes repo leak guards for tracked sensitive files and ignore-pattern coverage.
- Configured profile resources are local paths chosen by the user. Multicodex links them but does not execute or copy their contents.
- After successful login, regular auth file permissions are normalized to `0600`.

## Notes

- If your default Codex setup uses keychain auth only, configure file-backed auth for the profiles you want to use with multicodex.
- Do not copy, sync, transmit, transfer, or share Codex auth files between machines. Sign in locally with the official Codex login flow.

## License

Apache License 2.0. See `LICENSE`.
