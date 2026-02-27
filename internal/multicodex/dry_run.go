package multicodex

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func RenderDryRun(store *Store, cfg *Config, args []string) (string, error) {
	if len(args) == 0 {
		return renderDryRunOverview(store, cfg), nil
	}
	switch args[0] {
	case "use":
		if len(args) != 2 {
			return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run use <name>"}
		}
		return renderDryRunUse(cfg, args[1])
	case "login":
		if len(args) != 2 {
			return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run login <name>"}
		}
		return renderDryRunLogin(cfg, args[1])
	case "run":
		if len(args) < 4 {
			return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run run <name> -- <command...>"}
		}
		return renderDryRunRun(cfg, args[1], args[2:])
	case "switch-global":
		if len(args) != 2 {
			return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run switch-global <name>|--restore-default"}
		}
		if args[1] == "--restore-default" {
			return renderDryRunRestoreGlobal(store, cfg), nil
		}
		return renderDryRunSwitchGlobal(store, cfg, args[1])
	default:
		return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run [use|login|run|switch-global] ..."}
	}
}

func renderDryRunOverview(store *Store, cfg *Config) string {
	names := sortedProfileNames(cfg)
	profiles := "(none)"
	if len(names) > 0 {
		profiles = strings.Join(names, ", ")
	}
	var b strings.Builder
	b.WriteString("multicodex dry-run\n")
	b.WriteString("profiles: ")
	b.WriteString(profiles)
	b.WriteString("\n")
	b.WriteString("multicodex home: ")
	b.WriteString(store.paths.MulticodexHome)
	b.WriteString("\n")
	b.WriteString("default codex home: ")
	b.WriteString(store.paths.DefaultCodexHome)
	b.WriteString("\n\n")
	b.WriteString("planned sequence:\n")
	b.WriteString("1. init creates local multicodex directories and config only.\n")
	b.WriteString("2. add <name> creates an isolated profile CODEX_HOME and file-store config.\n")
	b.WriteString("3. login <name> runs official codex login within that profile context.\n")
	b.WriteString("4. use <name> outputs shell exports for current-terminal switching only.\n")
	b.WriteString("5. run <name> executes one command with that profile context only.\n")
	b.WriteString("6. switch-global <name> updates only default auth pointer plus backup metadata.\n")
	b.WriteString("7. switch-global --restore-default restores the original default auth state.\n\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String()
}

func renderDryRunUse(cfg *Config, name string) (string, error) {
	profile, ok := cfg.Profiles[name]
	if !ok {
		return "", &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	var b strings.Builder
	b.WriteString("multicodex dry-run use\n")
	b.WriteString("profile: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("would emit:\n")
	b.WriteString(RenderShellExports(profile.CodexHome, name))
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String(), nil
}

func renderDryRunLogin(cfg *Config, name string) (string, error) {
	profile, ok := cfg.Profiles[name]
	if !ok {
		return "", &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	var b strings.Builder
	b.WriteString("multicodex dry-run login\n")
	b.WriteString("profile: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("would run:\n")
	b.WriteString("CODEX_HOME=")
	b.WriteString(profile.CodexHome)
	b.WriteString(" codex login\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String(), nil
}

func renderDryRunRun(cfg *Config, name string, args []string) (string, error) {
	profile, ok := cfg.Profiles[name]
	if !ok {
		return "", &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep == len(args)-1 {
		return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run run <name> -- <command...>"}
	}
	command := strings.Join(args[sep+1:], " ")
	var b strings.Builder
	b.WriteString("multicodex dry-run run\n")
	b.WriteString("profile: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("would run:\n")
	b.WriteString("CODEX_HOME=")
	b.WriteString(profile.CodexHome)
	b.WriteString(" ")
	b.WriteString(command)
	b.WriteString("\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String(), nil
}

func renderDryRunSwitchGlobal(store *Store, cfg *Config, name string) (string, error) {
	profile, ok := cfg.Profiles[name]
	if !ok {
		return "", &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	profileAuth := filepath.Join(profile.CodexHome, "auth.json")
	backup := filepath.Join(store.paths.BackupsDir, "default-auth.backup")

	var b strings.Builder
	b.WriteString("multicodex dry-run switch-global\n")
	b.WriteString("target profile: ")
	b.WriteString(name)
	b.WriteString("\n\n")
	b.WriteString("would do:\n")
	b.WriteString("1. verify profile auth exists: ")
	b.WriteString(profileAuth)
	b.WriteString("\n")
	b.WriteString("2. if first switch, capture current default auth state for restore.\n")
	b.WriteString("3. remove current default auth path if it is a file or symlink: ")
	b.WriteString(store.paths.DefaultAuthPath)
	b.WriteString("\n")
	b.WriteString("4. create symlink: ")
	b.WriteString(store.paths.DefaultAuthPath)
	b.WriteString(" -> ")
	b.WriteString(profileAuth)
	b.WriteString("\n")
	b.WriteString("5. update multicodex metadata in config: global.current_profile = ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("6. backup file location when needed: ")
	b.WriteString(backup)
	b.WriteString("\n\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String(), nil
}

func renderDryRunRestoreGlobal(store *Store, cfg *Config) string {
	var b strings.Builder
	b.WriteString("multicodex dry-run switch-global --restore-default\n\n")
	b.WriteString("would do:\n")
	b.WriteString("1. read multicodex backup metadata from config.\n")
	b.WriteString("2. restore original default auth path at: ")
	b.WriteString(store.paths.DefaultAuthPath)
	b.WriteString("\n")
	if cfg.Global.BackupMode == "" {
		b.WriteString("3. backup mode is currently unknown or empty, so restore would likely no-op.\n")
	} else {
		b.WriteString("3. backup mode currently recorded: ")
		b.WriteString(cfg.Global.BackupMode)
		b.WriteString("\n")
	}
	b.WriteString("4. clear current global profile marker in multicodex config.\n\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String()
}

func sortedProfileNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
