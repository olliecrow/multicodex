package codexstate

import (
	"strings"
	"testing"
)

func TestSanitizedEnv(t *testing.T) {
	t.Parallel()

	env := SanitizedEnv([]string{
		"PATH=/bin",
		"CODEX_HOME=/stale",
		"MULTICODEX_ACTIVE_PROFILE=stale",
		"OPENAI_API_KEY=placeholder",
		"CODEX_AUTH_TOKEN=placeholder",
		"INVALID_ENTRY",
	}, "/isolated")
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"CODEX_HOME=/stale", "MULTICODEX_ACTIVE_PROFILE=", "OPENAI_API_KEY=", "CODEX_AUTH_TOKEN=", "INVALID_ENTRY"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("SanitizedEnv retained %q in %q", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "CODEX_HOME=/isolated") {
		t.Fatalf("SanitizedEnv removed required entries: %q", joined)
	}
}
