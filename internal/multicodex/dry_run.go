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
	case "login":
		if len(args) != 2 {
			return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run login <name>"}
		}
		return renderDryRunLogin(cfg, args[1])
	default:
		return "", &ExitError{Code: 2, Message: "usage: multicodex dry-run [login] ..."}
	}
}

func renderDryRunOverview(store *Store, cfg *Config) string {
	var b strings.Builder
	b.WriteString("multicodex dry-run\n")
	b.WriteString("configured profiles: ")
	b.WriteString(fmt.Sprintf("%d", len(cfg.Profiles)))
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
	b.WriteString("4. cli <name> starts an interactive Codex session with profile-local state.\n")
	b.WriteString("5. exec routes codex exec to a configured profile only.\n")
	b.WriteString("6. heartbeat sends one fixed read-only keepalive for logged-in profiles.\n")
	b.WriteString("7. multicodex does not switch or restore the shared default Codex auth account.\n\n")
	b.WriteString("dry-run only: no commands were executed and no files were changed.\n")
	return b.String()
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
	b.WriteString(shellQuoteValue(profile.CodexHome))
	b.WriteString(" codex login\n")
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
