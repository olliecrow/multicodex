# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine.

It keeps accounts isolated in named local profiles. You log in once per profile, then switch fast without repeated sign out and sign in, and watch subscription usage across accounts from one integrated terminal workflow.

By default it only changes the current terminal context. It does not change your system default Codex session unless you run an explicit global switch command.

## Current status

- Active and usable for local multi-account workflows.
- Still evolving; expect small command and UX refinements.

## Prerequisites

- Go (for building from source).
- Official `codex` CLI installed and available in `PATH`.

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

## Command reference

```text
multicodex init
multicodex add <name>
multicodex login <name> [codex login args]
multicodex login-all
multicodex use <name> [--shell]
multicodex run <name> -- <command...>
multicodex switch-global <name>
multicodex switch-global --restore-default
multicodex status
multicodex heartbeat
multicodex monitor [flags]
multicodex monitor doctor [--json] [--timeout 20s]
multicodex monitor completion [shell]
multicodex doctor [--json] [--timeout 8s]
multicodex dry-run [operation]
multicodex completion <bash|zsh|fish>
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

Switch system default account used by default Codex context.

```bash
multicodex switch-global work
```

Restore the original default account state.

```bash
multicodex switch-global --restore-default
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

```bash
multicodex monitor
multicodex monitor --interval 30s
multicodex monitor doctor
multicodex monitor completion
```

Monitor account resolution order:
- explicit account file under `~/multicodex/monitor/accounts.json`
- configured multicodex profiles from `~/multicodex/config.json`
- active `CODEX_HOME`
- compatible Codex homes discovered from the local filesystem

Legacy account-file paths are still read when the new multicodex monitor file is absent:
- `~/codex-usage-monitor/accounts.json`
- `~/.codex-usage-monitor/accounts.json`

For migration convenience, `multicodex monitor completion [shell]` is also supported and defaults to bash, matching the old standalone monitor habit.

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
