# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine without changing the normal default Codex account.

It keeps non-default accounts in named local profiles. Each profile has its own `CODEX_HOME`, auth file, sessions, threads, and Codex state. The regular system Codex account remains managed by Codex itself, outside multicodex.

By default, each profile reuses your global Codex `config.toml` through a symlink, so model defaults, reasoning settings, permission settings, and other normal Codex config changes apply everywhere. Profile homes also inherit missing top-level skill entries from the global Codex skills directory through symlinks.

Profile login requires file-backed auth. If the effective Codex config does not set `cli_auth_credentials_store = "file"`, profile login and profile-scoped Codex execution fail with a setup error instead of sharing global auth state.

## Status

- Usable for local multi-account Codex CLI, `codex exec`, heartbeat, and usage-monitor workflows.
- The command surface is intentionally narrow. Multicodex does not implement global account switching.

## Prerequisites

- Go 1.25 or newer for building from source.
- Development and CI checks use the patched Go toolchain listed in `go.mod`.
- Official `codex` CLI installed and available in `PATH`.
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

Run interactive Codex with one profile.

```bash
multicodex cli personal
multicodex cli work "check this repo"
```

Run `codex exec` on the best available account.

```bash
multicodex exec -s read-only "Summarize the README in 3 bullets."
```

Open the monitor and run checks.

```bash
multicodex monitor
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
multicodex cli <name> [codex args...]
multicodex exec [codex exec args]
multicodex status
multicodex reconcile
multicodex heartbeat
multicodex monitor [flags]
multicodex monitor tui [flags]
multicodex monitor doctor [flags]
multicodex monitor completion [shell]
multicodex doctor [--json] [--timeout 8s]
multicodex dry-run [operation]
multicodex completion <bash|zsh|fish>
multicodex version
multicodex help [command [subcommand]]
multicodex --version
```

## Interactive CLI

`multicodex cli <name> [codex args...]` launches the official `codex` CLI with that profile's `CODEX_HOME`.

Codex defaults such as model, reasoning level, approvals, sandbox, and search come from the shared Codex config unless you pass explicit Codex args. Multicodex does not inject its own model or permission defaults.

Two terminals can run `multicodex cli` with different profiles at the same time. Each terminal uses its own account, auth, threads, and `/goal` state because each one has a different `CODEX_HOME`.

## Exec Routing

`multicodex exec [codex exec args]` runs `codex exec` after selecting among configured multicodex profiles, with the default Codex home as a built-in reserve account.

- Help requests such as `multicodex exec --help` delegate directly to `codex exec` and do not require profiles.
- Exec can run with no configured profiles by using the default Codex home as the only available account.
- Configured profiles at 100% weekly usage are not selected.
- Exec uses configured selection priority first, then prefers the profile whose known weekly reset is soonest.
- Profiles with an unknown weekly reset follow profiles with a known reset. Exact ties are randomized.
- The default Codex home is a protected reserve. It is used only when no configured profile has usable weekly usage.
- If the default Codex home is the only remaining destination, exec uses it as the final fallback even when its usage data is unavailable or exhausted.
- For explicit Spark model names, configured profiles need Spark usage windows to win normal routing; the default Codex home still remains the final fallback.

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
