# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine.

It keeps accounts isolated in named local profiles. You log in once per profile, then switch fast without repeated sign out and sign in, and watch subscription usage across accounts from one integrated terminal workflow.

By default, each profile reuses your global Codex `config.toml`, so normal Codex settings changes continue to apply across all multicodex profiles. A profile can still opt into its own config by replacing its profile-local `config.toml`.

Profile login still requires file-backed auth. If your shared global Codex config does not set `cli_auth_credentials_store = "file"`, `multicodex login` will fail with a setup error until you either enable file-backed auth globally or create a per-profile override. The same preflight is re-checked before profile-scoped Codex execution (`multicodex cli`, `multicodex exec`, `multicodex heartbeat`, and direct `multicodex run ... -- codex ...`), so later edits to the shared config cannot silently weaken isolation.

Multicodex does not switch, restore, or otherwise manage the shared default Codex auth account. Keep the normal system Codex account, such as `crowoy`, configured through Codex itself.

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
- You can override the state location with `MULTICODEX_HOME`.
- Profile auth stays isolated under `~/multicodex/profiles/<name>/codex-home/auth.json`.
- Profile `auth.json` must be a regular profile-local file; auth symlinks fail login hardening and doctor checks.
- Profile-scoped CLI, exec, and run sessions keep Codex state, including thread and `/goal` state, under `~/multicodex/profiles/<name>/codex-home/`.
- Profile config defaults to a symlink from `~/multicodex/profiles/<name>/codex-home/config.toml` to your default Codex config at `~/.codex/config.toml`.
- Profile skills fill in missing top-level entries from `~/.codex/skills` so shared skills stay visible in profile-scoped Codex runs.
- If you want a per-profile skill override, create that top-level entry inside the profile's `codex-home/skills` directory and multicodex will leave it alone.
- If you want a per-profile Codex config, replace that symlink with a regular `config.toml` file in the profile's `codex-home`.

## Command reference

```text
multicodex init
multicodex add <name>
multicodex login <name> [codex login args]
multicodex login-all
multicodex use <name> [--shell]
multicodex cli <name> [codex args...]
multicodex run <name> -- <command...>
multicodex exec [codex exec args]
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

Run the normal interactive Codex CLI with one profile.

```bash
multicodex cli personal
multicodex cli work "check this repo"
```

`multicodex cli <name>` is the profile-scoped version of the local `c` alias. It runs `codex --search --dangerously-bypass-approvals-and-sandbox -m gpt-5.5 -c model_reasoning_effort=medium` with that profile's `CODEX_HOME`, then appends any extra args you pass after the profile name. It does not change the shared global account.
When launched from a real interactive terminal, `multicodex cli` hands off directly into `codex` so the live process behaves like a normal Codex CLI session instead of staying wrapped under a long-lived multicodex parent process.
Two terminals can run `multicodex cli` with different profiles at the same time. Each one gets its own account, threads, and `/goal` state because each one has a different `CODEX_HOME`.

Run `codex exec` on the best available logged-in profile automatically.

```bash
multicodex exec -s read-only "Summarize the README in 3 bullets."
```

`multicodex exec` first keeps profiles whose five-hour window is below 40% used and whose weekly window is not known to be exhausted. From those eligible profiles, it picks the one whose weekly reset is soonest. If eligible profiles do not expose a weekly reset time, it picks randomly among those eligible profiles. If no profile is eligible, it picks a random accessible profile for that call. If usage data is unavailable for every profile, it falls back to a random configured profile.
For explicit Spark models (`--model`/`-m` containing `spark`), `multicodex exec` uses Spark buckets for routing and fails rather than falling back to a random/default Codex account when Spark data is not available.
For help requests such as `multicodex exec --help`, it delegates directly to `codex exec` and does not require any profiles to be configured.

Run non-mutating checks and preview commands.

```bash
multicodex doctor
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

This confirms the profile can complete an actual request without changing the default Codex account.

Send a fire-and-forget keepalive hello to every logged-in profile.

```bash
multicodex heartbeat
```

Heartbeat runs stay profile-local: they use each profile's `CODEX_HOME` and run `codex exec` in read-only mode.

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
It orders account rows by weekly reset time, from first to reset at the top to last at the bottom.
Each window card renders usage as compact lines such as `used: 12% [resets in 3h4m]`; when Spark data is present it adds `used-spark: 8% [resets in 3h4m]` in the same card instead of adding another card.
It defaults both the refresh interval and fetch timeout to 60 seconds.

```bash
multicodex monitor
multicodex monitor tui
multicodex monitor --interval 30s
multicodex monitor doctor
multicodex monitor completion
```

Monitor account candidates come from:
- explicit account file under `~/multicodex/monitor/accounts.json`
- configured multicodex profiles from `~/multicodex/config.json`
- the default Codex home, respecting `MULTICODEX_DEFAULT_CODEX_HOME` when it is set and otherwise `CODEX_HOME`
- the active `CODEX_HOME`
- compatible Codex homes discovered from the local filesystem

When the same Codex home is found more than once, labels and source details prefer the explicit monitor account file, then configured multicodex profiles, then the default Codex home, then the active `CODEX_HOME`, then auto-discovery. TUI row ordering is separate from discovery precedence: rows are ordered by weekly reset time, not by account source.

Filesystem discovery is read-only but intentionally broad: it scans your home directory for `.codex*`, `.codex`, and `codex-home` directories up to depth 5, then filters known transient/cache locations and requires real usage signals before including a home.

`multicodex monitor completion [shell]` prints the same shell completion script as `multicodex completion [shell]` and defaults to bash.

`multicodex monitor doctor` succeeds when at least one usage source works. A passing result can still be degraded if either the app-server path or the OAuth fallback is unavailable.
When fallback is available, the monitor keeps most of a long fetch timeout for the main source and reserves at most 10 seconds for fallback, which helps cut false `unavailable` window cards on slower refreshes.
When a refresh loses official window data for every account at once, the TUI keeps showing the last good official window cards, marks them as stale, and keeps the latest local token estimate and refresh warnings visible.
When a profile login has expired, the monitor prefers a short diagnostics warning that tells you to sign in again instead of only showing a long raw fetch error.
Some accounts only expose one official usage window. When that happens, the monitor keeps the account visible, shows the window that is present, and marks the missing window as unavailable instead of treating the whole account as failed.

Observed token totals shown by the monitor are local estimates derived from session logs. Treat them as advisory and separate from the official five-hour and weekly windows. The TUI shows the weekly local estimate, labels it as a token estimate, and can show partial results when some account-home estimates are unavailable.

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
- Does not change, restore, back up, or symlink the shared default Codex auth account.
- `monitor` is read-only and does not mutate Codex account data.
- `doctor` and `dry-run` are non-mutating helpers.
- `doctor` includes repo leak guards for tracked sensitive files and ignore-pattern coverage.
- After successful login, regular auth file permissions are normalized to `0600`; auth symlinks are rejected.

## Notes

- Profile auth is isolated by profile `CODEX_HOME`.
- If your default Codex setup uses keychain auth only, configure file-backed auth for the profiles you want to use with multicodex.

## License

Apache License 2.0. See `LICENSE`.

<!-- third-party-policy:start -->
## Third-Party Code Policy
This repository allows external-code snapshots for static analysis only. External clones must stay in ephemeral `plan/` locations, be sanitized immediately (`rm -rf .git`, or remove all remotes first if `.git` is temporarily retained), and must never be executed.

See `docs/untrusted-third-party-repos.md`.
<!-- third-party-policy:end -->
