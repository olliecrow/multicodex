package codexappserver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInspectGenerationConfig(t *testing.T) {
	home := t.TempDir()
	config := `
model = "test-model"

[mcp_servers.zeta]
url = "https://example.invalid/mcp"

[mcp_servers."alpha.with.dot"]
command = "synthetic-command"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := inspectGenerationConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha.with.dot", "zeta"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
}

func TestInspectGenerationConfigMissingConfig(t *testing.T) {
	names, err := inspectGenerationConfig(t.TempDir())
	if err != nil || len(names) != 0 {
		t.Fatalf("names = %q, error = %v", names, err)
	}
}

func TestInspectGenerationConfigSanitizesDecodeError(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`private-value = "`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := inspectGenerationConfig(home)
	if err == nil || !strings.Contains(err.Error(), "decode Codex configuration") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("error exposed configuration content: %v", err)
	}
}

func TestInspectGenerationConfigRejectsOpenAIProviderOverrides(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
	}{
		{name: "provider definition", config: "[model_providers.openai]\nbase_url = \"https://example.invalid\"\n"},
		{name: "base URL", config: "openai_base_url = \"https://example.invalid\"\n"},
		{name: "ChatGPT base URL", config: "chatgpt_base_url = \"https://example.invalid\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectGenerationConfig(home); err == nil || !strings.Contains(err.Error(), "OpenAI provider") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspectGenerationConfigRejectsConfigLockfile(t *testing.T) {
	home := t.TempDir()
	config := "[debug.config_lockfile]\nload_path = \"/synthetic/config.lock.toml\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectGenerationConfig(home); err == nil || !strings.Contains(err.Error(), "lockfile") {
		t.Fatalf("error = %v", err)
	}
}
