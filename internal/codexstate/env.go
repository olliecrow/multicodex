package codexstate

import "strings"

// SanitizedEnv removes inherited profile and account overrides before an
// isolated Codex process is started. When codexHome is non-empty, it becomes
// the process's sole CODEX_HOME.
func SanitizedEnv(base []string, codexHome string) []string {
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || environmentVariableIsAccountScoped(key) {
			continue
		}
		env = append(env, entry)
	}
	if codexHome != "" {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	return env
}

func environmentVariableIsAccountScoped(key string) bool {
	switch key {
	case "CODEX_HOME",
		"MULTICODEX_ACTIVE_PROFILE",
		"MULTICODEX_SELECTED_PROFILE_PATH",
		"MULTICODEX_HEARTBEAT_LOCK_PATH",
		"MULTICODEX_HEARTBEAT_PROMPT",
		"OPENAI_API_KEY",
		"OPENAI_ORG_ID",
		"OPENAI_ORGANIZATION",
		"OPENAI_PROJECT",
		"OPENAI_BASE_URL",
		"OPENAI_API_BASE",
		"OPENAI_HOST",
		"CODEX_API_KEY",
		"CODEX_AUTH_TOKEN",
		"CODEX_ACCESS_TOKEN",
		"CODEX_REFRESH_TOKEN",
		"CODEX_TOKEN",
		"CODEX_BASE_URL",
		"CODEX_API_BASE":
		return true
	default:
		return false
	}
}
