package multicodex

import (
	"fmt"
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
	default:
		return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run [use|login|run] ..."}
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
	b.WriteString("2. add <name> creates an isolated profile CODEX_HOME and links profile config to the default Codex config by default.\n")
	b.WriteString("3. login <name> runs official codex login within that profile context.\n")
	b.WriteString("4. use <name> outputs shell exports for current-terminal switching only.\n")
	b.WriteString("5. run <name> executes one command with that profile context only.\n")
	b.WriteString("6. multicodex does not switch or restore the shared default Codex auth account.\n\n")
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
	var b strings.Builder
	b.WriteString("multicodex dry-run run\n")
	b.WriteString("profile: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("would run:\n")
	b.WriteString("CODEX_HOME=")
	b.WriteString(shellQuoteValue(profile.CodexHome))
	for _, arg := range args[sep+1:] {
		b.WriteString(" ")
		b.WriteString(shellQuoteValue(arg))
	}
	b.WriteString("\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String(), nil
}

func sortedProfileNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
