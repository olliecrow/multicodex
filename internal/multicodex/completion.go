package multicodex

import (
	"fmt"
	"strings"
)

func (a *App) cmdCompletion(args []string) error {
	if len(args) != 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex completion <bash|zsh|fish>"}
	}

	switch args[0] {
	case "bash":
		fmt.Print(renderBashCompletion())
	case "zsh":
		fmt.Print(renderZshCompletion())
	case "fish":
		fmt.Print(renderFishCompletion())
	default:
		return &ExitError{Code: 2, Message: "unsupported shell. expected one of: bash, zsh, fish"}
	}
	return nil
}

func (a *App) cmdCompleteProfiles() error {
	cfg, err := a.loadConfigIfExists()
	if err != nil {
		return nil
	}
	names := sortedProfileNames(cfg)
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}

func renderBashCompletion() string {
	return strings.TrimSpace(`
_multicodex_profiles() {
  local bin="${COMP_WORDS[0]}"
  "$bin" __complete-profiles 2>/dev/null
}

_multicodex_complete() {
  local cur prev cmd
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev=""
  if (( COMP_CWORD > 0 )); then
    prev="${COMP_WORDS[COMP_CWORD-1]}"
  fi
  cmd="${COMP_WORDS[1]:-}"

  local commands="init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help"

  if (( COMP_CWORD == 1 )); then
    COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
    return 0
  fi

  case "$cmd" in
    add|login|use)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "$(_multicodex_profiles)" -- "$cur") )
        return 0
      fi
      if [[ "$cmd" == "use" ]] && (( COMP_CWORD == 3 )); then
        COMPREPLY=( $(compgen -W "--shell" -- "$cur") )
        return 0
      fi
      ;;
    run)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "$(_multicodex_profiles)" -- "$cur") )
        return 0
      fi
      if (( COMP_CWORD == 3 )); then
        COMPREPLY=( $(compgen -W "--" -- "$cur") )
        return 0
      fi
      ;;
    switch-global)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "$(_multicodex_profiles) --restore-default" -- "$cur") )
        return 0
      fi
      ;;
    monitor)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "doctor completion help tui --interval --timeout --no-color --no-alt-screen" -- "$cur") )
        return 0
      fi
      if (( COMP_CWORD >= 3 )); then
        case "${COMP_WORDS[2]}" in
          completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
            return 0
            ;;
          doctor)
            COMPREPLY=( $(compgen -W "--json --timeout" -- "$cur") )
            return 0
            ;;
          help)
            COMPREPLY=( $(compgen -W "doctor completion tui" -- "$cur") )
            return 0
            ;;
          tui)
            COMPREPLY=( $(compgen -W "--interval --timeout --no-color --no-alt-screen" -- "$cur") )
            return 0
            ;;
          *)
            COMPREPLY=( $(compgen -W "--interval --timeout --no-color --no-alt-screen doctor completion help tui" -- "$cur") )
            return 0
            ;;
        esac
      fi
      ;;
    doctor)
      COMPREPLY=( $(compgen -W "--json --timeout" -- "$cur") )
      return 0
      ;;
    dry-run)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "use login run switch-global" -- "$cur") )
        return 0
      fi
      ;;
    completion)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
        return 0
      fi
      ;;
    help)
      if (( COMP_CWORD == 2 )); then
        COMPREPLY=( $(compgen -W "init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help monitor\ doctor monitor\ completion monitor\ tui" -- "$cur") )
        return 0
      fi
      ;;
  esac
}

complete -F _multicodex_complete multicodex
`) + "\n"
}

func renderZshCompletion() string {
	return strings.TrimSpace(`
autoload -U +X compinit && compinit
autoload -U +X bashcompinit && bashcompinit

_multicodex_profiles() {
  local bin="${words[1]:-multicodex}"
  "$bin" __complete-profiles 2>/dev/null
}

_multicodex_complete() {
  local cur prev cmd
  cur="${words[CURRENT]}"
  prev=""
  if (( CURRENT > 1 )); then
    prev="${words[CURRENT-1]}"
  fi
  cmd="${words[2]:-}"

  local commands="init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help"

  if (( CURRENT == 2 )); then
    compadd -- ${=commands}
    return
  fi

  case "$cmd" in
    add|login|use)
      if (( CURRENT == 3 )); then
        compadd -- ${=($(_multicodex_profiles))}
        return
      fi
      if [[ "$cmd" == "use" ]] && (( CURRENT == 4 )); then
        compadd -- --shell
        return
      fi
      ;;
    run)
      if (( CURRENT == 3 )); then
        compadd -- ${=($(_multicodex_profiles))}
        return
      fi
      if (( CURRENT == 4 )); then
        compadd -- --
        return
      fi
      ;;
    switch-global)
      if (( CURRENT == 3 )); then
        compadd -- ${=($(_multicodex_profiles))} --restore-default
        return
      fi
      ;;
    monitor)
      if (( CURRENT == 3 )); then
        compadd -- doctor completion help tui --interval --timeout --no-color --no-alt-screen
        return
      fi
      case "${words[3]:-}" in
        completion)
          compadd -- bash zsh fish
          return
          ;;
        doctor)
          compadd -- --json --timeout
          return
          ;;
        help)
          compadd -- doctor completion tui
          return
          ;;
        tui)
          compadd -- --interval --timeout --no-color --no-alt-screen
          return
          ;;
        *)
          compadd -- doctor completion help tui --interval --timeout --no-color --no-alt-screen
          return
          ;;
      esac
      ;;
    doctor)
      compadd -- --json --timeout
      return
      ;;
    dry-run)
      if (( CURRENT == 3 )); then
        compadd -- use login run switch-global
        return
      fi
      ;;
    completion)
      if (( CURRENT == 3 )); then
        compadd -- bash zsh fish
        return
      fi
      ;;
    help)
      if (( CURRENT == 3 )); then
        compadd -- init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help "monitor doctor" "monitor completion" "monitor tui"
        return
      fi
      ;;
  esac
}

compdef _multicodex_complete multicodex
`) + "\n"
}

func renderFishCompletion() string {
	return strings.TrimSpace(`
function __multicodex_profiles
    multicodex __complete-profiles 2>/dev/null
end

complete -c multicodex -f -n '__fish_use_subcommand' -a 'init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help'
complete -c multicodex -f -n '__fish_seen_subcommand_from add login use' -a '(__multicodex_profiles)'
complete -c multicodex -f -n '__fish_seen_subcommand_from run' -a '(__multicodex_profiles)'
complete -c multicodex -f -n '__fish_seen_subcommand_from switch-global' -a '(__multicodex_profiles) --restore-default'
complete -c multicodex -f -n '__fish_seen_subcommand_from monitor' -a 'doctor completion help tui'
complete -c multicodex -f -n '__fish_seen_subcommand_from monitor' -l interval
complete -c multicodex -f -n '__fish_seen_subcommand_from monitor' -l timeout
complete -c multicodex -f -n '__fish_seen_subcommand_from monitor' -l no-color
complete -c multicodex -f -n '__fish_seen_subcommand_from monitor' -l no-alt-screen
complete -c multicodex -f -n '__fish_seen_subcommand_from completion; and __fish_seen_subcommand_from monitor' -a 'bash zsh fish'
complete -c multicodex -f -n '__fish_seen_subcommand_from dry-run' -a 'use login run switch-global'
complete -c multicodex -f -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c multicodex -f -n '__fish_seen_subcommand_from help' -a 'init add login login-all use run switch-global status heartbeat monitor doctor dry-run completion version help "monitor doctor" "monitor completion" "monitor tui"'
complete -c multicodex -f -n '__fish_seen_subcommand_from use' -l shell
complete -c multicodex -f -n '__fish_seen_subcommand_from doctor' -l json
complete -c multicodex -f -n '__fish_seen_subcommand_from doctor' -l timeout
complete -c multicodex -f -n '__fish_seen_subcommand_from doctor; and __fish_seen_subcommand_from monitor' -l json
complete -c multicodex -f -n '__fish_seen_subcommand_from doctor; and __fish_seen_subcommand_from monitor' -l timeout
`) + "\n"
}
