# multicodex

`multicodex` helps you use multiple Codex subscription accounts on one machine.

It keeps accounts isolated in named local profiles. You log in once per profile, then switch fast without repeated sign out and sign in.

By default it only changes the current terminal context. It does not change your system default Codex session unless you run an explicit global switch command.

## Current status

- Active and usable for local multi-account workflows.
- Still evolving; expect small command and UX refinements.

## Prerequisites

- Go (for building from source).
- Official `codex` CLI installed and available in `PATH`.

## Why this exists

If you have more than one Codex subscription account, switching accounts can be annoying. This tool gives you a simple local workflow that stays compatible with normal `codex login`.

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

6. Run setup checks before using it daily.

```bash
multicodex doctor
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
multicodex doctor [--json] [--timeout 8s]
multicodex dry-run [operation]
multicodex completion <bash|zsh|fish>
multicodex help [command]
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

Send a fire-and-forget keepalive hello to every logged-in profile.

```bash
multicodex heartbeat
```

For periodic refresh, add this command to your cron schedule.

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
multicodex help completion
```

## Safety model

- Uses official `codex login` flows.
- Keeps profile state local on disk.
- Does not send secrets to third-party services.
- Does not store raw secrets in multicodex config.
- Global switch touches only the default auth pointer path.
- `doctor` and `dry-run` are non-mutating helpers.
- `doctor` includes repo leak guards for tracked sensitive files and ignore-pattern coverage.
- After successful login, auth file permissions are normalized to `0600`.

## Notes

- Profile auth is isolated by profile `CODEX_HOME`.
- Global switching is explicit. It is never the default.
- If your default Codex setup uses keychain auth only, global auth pointer switching might not affect every context. In that case configure default Codex auth storage to file mode.

## License

Apache License 2.0. See `LICENSE`.
