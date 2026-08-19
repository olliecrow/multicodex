package codexappserver

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

const maxCodexConfigBytes = 16 * 1024 * 1024

func inspectGenerationConfig(codexHome string, webSearch bool) ([]string, error) {
	file, err := os.Open(filepath.Join(codexHome, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read Codex configuration")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCodexConfigBytes+1))
	if err != nil {
		return nil, errors.New("read Codex configuration")
	}
	if len(data) > maxCodexConfigBytes {
		return nil, errors.New("Codex configuration exceeds safety limit")
	}
	var config struct {
		MCPServers     map[string]any `toml:"mcp_servers"`
		ModelProviders map[string]any `toml:"model_providers"`
		OpenAIBaseURL  *string        `toml:"openai_base_url"`
		ChatGPTBaseURL *string        `toml:"chatgpt_base_url"`
		Debug          struct {
			ConfigLockfile struct {
				LoadPath *string `toml:"load_path"`
			} `toml:"config_lockfile"`
		} `toml:"debug"`
		Tools struct {
			WebSearch any `toml:"web_search"`
		} `toml:"tools"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, errors.New("decode Codex configuration")
	}
	if _, overridden := config.ModelProviders[subscriptionProvider]; overridden {
		return nil, errors.New("generate cannot use a configuration that replaces the built-in OpenAI provider")
	}
	if config.OpenAIBaseURL != nil || config.ChatGPTBaseURL != nil {
		return nil, errors.New("generate cannot use a configuration that overrides the OpenAI provider endpoint")
	}
	if config.Debug.ConfigLockfile.LoadPath != nil {
		return nil, errors.New("generate cannot use a Codex configuration lockfile")
	}
	if webSearch {
		if settings, ok := config.Tools.WebSearch.(map[string]any); ok && len(settings) != 0 {
			return nil, errors.New("generate --search cannot use inherited web-search settings")
		}
	}

	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
