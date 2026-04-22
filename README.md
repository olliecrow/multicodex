# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine.

It keeps accounts isolated in named local profiles. You log in once per profile, then switch fast without repeated sign out and sign in, and watch subscription usage across accounts from one integrated terminal workflow.

By default, each profile reuses your global Codex `config.toml`, so normal Codex settings changes continue to apply across all multicodex profiles. A profile can still opt into its own config by replacing its profile-local `config.toml`.

Profile login still requires file-backed auth. If your shared global Codex config does not set `cli_auth_credentials_store = "file"`, `multicodex login` will fail with a setup error until you either enable file-backed auth globally or create a per-profile override. The same preflight is re-checked before profile-scoped Codex execution (`multicodex exec`, `multicodex heartbeat`, direct `multicodex run ... -- codex ...`, and `multicodex switch-global` unless you explicitly force it), so later edits to the shared config cannot silently weaken isolation.

Most commands only change the current terminal context. `multicodex app` and `multicodex switch-global` are the two commands that also update the shared default Codex auth pointer on purpose.

## Current status

- Active and usable for local multi-account workflows.
- Still evolving; expect small command and UX refinements.

## Prerequisites

- Go 1.24 or newer (for building from source).
- Official `codex` CLI installed and available in `PATH`.
- supported host platforms: macOS and Linux only

## Why this exists

If you have more than one Codex subscription account, switching accounts can be annoying, and it is even harder when you do not know which account has headroom left. This tool gives you one local workflow for profile isolation, fast switching, and live subscription usage visibility while staying compatible with normal `codex login`.

## Install

Build from source.

```bash
go build -o multicodex ./cmd/multicodex
```

Optional for shell usage.

```bash
mv ./multicodex ~/.local/bin/multicodex
```

## Quick start

1. Initialize local state.

```bash
multicodex init
```

2. Add profiles.

```bash
multicodex add personal
multicodex add work
```

3. Log in to each profile once.

```bash
multicodex login personal
multicodex login work
```

4. Switch current terminal to one profile.

```bash
eval "$(multicodex use personal)"
```

5. Check status across all profiles.

```bash
multicodex status
```

6. Open the live usage monitor.

```bash
multicodex monitor
```

7. Run setup checks before using it daily.

```bash
multicodex doctor
multicodex monitor doctor
multicodex dry-run
```

## Local state directory

- Default multicodex state home is `~/multicodex`.
- If `~/.multicodex` exists and `~/multicodex` does not, multicodex automatically migrates existing state on first run.
- You can override the state location with `MULTICODEX_HOME`.
- Profile auth stays isolated under `~/multicodex/profiles/<name>/codex-home/auth.json`.
- Profile config defaults to a symlink from `~/multicodex/profiles/<name>/codex-home/config.toml` to your default Codex config at `~/.codex/config.toml`.
- If you want a per-profile Codex config, replace that symlink with a regular `config.toml` file in the profile's `codex-home`.

## Command reference

```text
multicodex init
multicodex add <name>
multicodex login <name> [codex login args]
multicodex login-all
multicodex use <name> [--shell]
multicodex app <name>
multicodex run <name> -- <command...>
multicodex exec [codex exec args]
multicodex switch-global <name> [--force]
multicodex switch-global --restore-default
multicodex status
multicodex heartbeat
multicodex monitor [flags]
multicodex monitor help
multicodex monitor tui [flags]
multicodex monitor doctor [--json] [--timeout 60s]
multicodex monitor completion [shell]
multicodex doctor [--json] [--timeout 8s]
multicodex dry-run [operation]
multicodex completion <bash|zsh|fish>
multicodex version
multicodex help [command [subcommand]]
multicodex --version
```

## Common workflows

Local only workflow for this terminal.

```bash
eval "$(multicodex use work)"
codex
```

Run one command in another profile without changing your shell.

```bash
multicodex run personal -- codex login status
```

Launch a new Codex Mac app instance for one profile while keeping one shared left-side list.

```bash
multicodex app personal
multicodex app work
```

`multicodex app` is for macOS. It first switches the shared default auth pointer to that profile, then launches `Codex.app` with the shared default `CODEX_HOME` and a stable per-profile app-data folder under `~/Library/Application Support/Codex-multicodex/<profile>`. That keeps one shared sidebar state while avoiding one giant shared Electron app-data folder across every app window. `multicodex` exits after handing the launch off to macOS; it does not keep a helper process running. Already-open app windows usually keep the account they started with, but that split is best-effort rather than a hard lock because Codex can reload auth later in some flows.

Run `codex exec` on the best available logged-in profile automatically.

```bash
multicodex exec -s read-only "Summarize the README in 3 bullets."
```

`multicodex exec` first keeps profiles whose five-hour window is below 40% used. From those eligible profiles, it picks the one whose weekly reset is soonest. If eligible profiles do not expose a weekly reset time, it picks randomly among those eligible profiles. If no profile is eligible, it picks a random accessible profile for that call. If usage data is unavailable for every profile, it falls back to a random configured profile.
For help requests such as `multicodex exec --help`, it delegates directly to `codex exec` and does not require any profiles to be configured.

Switch system default account used by default Codex context.

```bash
multicodex switch-global work
```

Restore the latest saved non-multicodex-managed default account state.

```bash
multicodex switch-global --restore-default
```

If you deliberately need to bypass the file-backed-auth preflight, use `--force`.

```bash
multicodex switch-global --force work
```

Run non-mutating checks and preview commands.

```bash
multicodex doctor
multicodex dry-run switch-global work
multicodex dry-run use personal
multicodex dry-run run work -- codex login status
```

Verify a newly added profile with a real read-only Codex task.

```bash
multicodex status
multicodex run work -- codex login status
multicodex run work -- codex exec -s read-only -C /path/to/repo \
  "Summarize the README in 3 bullets."
multicodex status
```

This confirms the profile can complete an actual request while keeping the global default unchanged.

Send a fire-and-forget keepalive hello to every logged-in profile.

```bash
multicodex heartbeat
```

Heartbeat runs stay profile-local: they use each profile's `CODEX_HOME`, run `codex exec` in read-only mode, and do not switch the global default account.

For cron use, heartbeat also:
- skips overlapping runs via a lock file under `~/multicodex`
- retries failed profile heartbeats once with linear backoff by default
- keeps failure output redacted to avoid leaking raw CLI details

Optional environment overrides:
- `MULTICODEX_HEARTBEAT_TIMEOUT_SECONDS`
- `MULTICODEX_HEARTBEAT_RETRIES`
- `MULTICODEX_HEARTBEAT_BACKOFF_SECONDS`
- `MULTICODEX_HEARTBEAT_PROMPT`
- `MULTICODEX_HEARTBEAT_LOCK_PATH`

For periodic refresh, add this command to your cron schedule, for example:

```bash
0 */6 * * * PATH=/path/to/bin:$PATH; multicodex heartbeat >> /path/to/logs/heartbeat-cron.log 2>&1
```

Monitor live subscription usage across your configured and discovered accounts.
The TUI identifies accounts by configured account label and renders user-facing timestamps in local time without seconds.
It defaults both the refresh interval and fetch timeout to 60 seconds.

```bash
multicodex monitor
multicodex monitor tui
multicodex monitor --interval 30s
multicodex monitor doctor
multicodex monitor completion
```

Monitor account resolution order:
- explicit account file under `~/multicodex/monitor/accounts.json`
- configured multicodex profiles from `~/multicodex/config.json`
- active `CODEX_HOME`
- default Codex home at `~/.codex`
- compatible Codex homes discovered from the local filesystem

Filesystem discovery is read-only but intentionally broad: it scans your home directory for `.codex*`, `.codex`, and `codex-home` directories up to depth 5, then filters known transient/cache locations and requires real usage signals before including a home.

Legacy account-file paths are still read when the new multicodex monitor file is absent:
- `~/codex-usage-monitor/accounts.json`
- `~/.codex-usage-monitor/accounts.json`

For migration convenience, `multicodex monitor completion [shell]` is also supported and defaults to bash, matching the old standalone monitor habit.

`multicodex monitor doctor` succeeds when at least one usage source works. A passing result can still be degraded if either the app-server path or the OAuth fallback is unavailable.
When fallback is available, the monitor keeps most of a long fetch timeout for the main source and reserves at most 10 seconds for fallback, which helps cut false `unavailable` window cards on slower refreshes.
When a refresh loses official window data for every account at once, the TUI keeps showing the last good official window cards, marks them as stale, and keeps the latest local token estimate and refresh warnings visible.
When a profile login has expired, the monitor prefers a short diagnostics warning that tells you to sign in again instead of only showing a long raw fetch error.
Some accounts only expose one official usage window. When that happens, the monitor keeps the account visible, shows the window that is present, and marks the missing window as unavailable instead of treating the whole account as failed.

Observed token totals shown by the monitor are local estimates derived from session logs. Treat them as advisory and separate from the official five-hour and weekly windows. The TUI labels them as token estimates and can show partial results when some account-home estimates are unavailable.

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

Enable tab autocomplete.

```bash
# zsh for current shell
eval "$(multicodex completion zsh)"

# bash for current shell
eval "$(multicodex completion bash)"

# fish persistent completion file
multicodex completion fish > ~/.config/fish/completions/multicodex.fish
```

Get detailed help for any command.

```bash
multicodex help
multicodex help heartbeat
multicodex help monitor
multicodex help monitor doctor
multicodex help monitor completion
multicodex help monitor tui
multicodex help completion
```

## Development checks

```bash
go test ./...
go test -race ./...
go vet ./...
go build -o multicodex ./cmd/multicodex
```

## Safety model

- Uses official `codex login` flows.
- Keeps profile state local on disk.
- Does not send secrets to third-party services.
- Does not store raw secrets in multicodex config.
- Global switch touches only the default auth pointer path.
- `monitor` is read-only and does not mutate Codex account data.
- `doctor` and `dry-run` are non-mutating helpers.
- `doctor` includes repo leak guards for tracked sensitive files and ignore-pattern coverage.
- After successful login, auth file permissions are normalized to `0600`.

## Notes

- Profile auth is isolated by profile `CODEX_HOME`.
- Global switching is explicit. It is never the default.
- If your default Codex setup uses keychain auth only, global auth pointer switching might not affect every context. In that case configure default Codex auth storage to file mode.

## License

Apache License 2.0. See `LICENSE`.

<!-- third-party-policy:start -->
## Third-Party Code Policy
This repository allows external-code snapshots for static analysis only. External clones must stay in ephemeral `plan/` locations, be sanitized immediately (`rm -rf .git`, or remove all remotes first if `.git` is temporarily retained), and must never be executed.

See `docs/untrusted-third-party-repos.md`.
<!-- third-party-policy:end -->
