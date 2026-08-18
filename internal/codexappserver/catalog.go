package codexappserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/olliecrow/multicodex/internal/codexstate"
)

const (
	SupportedCodexVersion = "codex-cli 0.147.0"
	maxCatalogBytes       = 16 * 1024 * 1024
)

type CatalogOptions struct {
	Command         []string
	BaseEnv         []string
	CodexHome       string
	ActiveProfile   string
	RequestedModel  string
	RequestedEffort string
	OutputPath      string
}

type CatalogSelection struct {
	Model  string
	Effort string
}

type rawCatalog struct {
	Models []map[string]any `json:"models"`
}

func PrepareToolFreeCatalog(ctx context.Context, options CatalogOptions) (CatalogSelection, error) {
	command := options.Command
	if len(command) == 0 {
		command = []string{defaultCommand}
	}
	env := isolatedEnv(options.BaseEnv, options.CodexHome, options.ActiveProfile)
	versionOutput, err := runBounded(ctx, command, []string{"--version"}, env, 1024)
	if err != nil {
		return CatalogSelection{}, fmt.Errorf("check Codex compatibility: %w", err)
	}
	version := strings.TrimSpace(string(versionOutput))
	if version != SupportedCodexVersion {
		return CatalogSelection{}, fmt.Errorf("unsupported Codex version; generate requires %s", SupportedCodexVersion)
	}

	catalogOutput, err := runBounded(ctx, command, []string{"debug", "models", "--bundled"}, env, maxCatalogBytes)
	if err != nil {
		return CatalogSelection{}, fmt.Errorf("read Codex model catalog: %w", err)
	}
	var catalog rawCatalog
	if err := json.Unmarshal(catalogOutput, &catalog); err != nil {
		return CatalogSelection{}, fmt.Errorf("decode Codex model catalog: %w", err)
	}
	model, name, err := selectCatalogModel(catalog.Models, options.RequestedModel)
	if err != nil {
		return CatalogSelection{}, err
	}
	effort, err := selectReasoningEffort(model, options.RequestedEffort)
	if err != nil {
		return CatalogSelection{}, err
	}
	if err := removeAgentTools(model); err != nil {
		return CatalogSelection{}, fmt.Errorf("selected model is not compatible with tool-free generation: %w", err)
	}

	encoded, err := json.Marshal(rawCatalog{Models: []map[string]any{model}})
	if err != nil {
		return CatalogSelection{}, fmt.Errorf("encode tool-free model catalog: %w", err)
	}
	file, err := os.OpenFile(options.OutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return CatalogSelection{}, fmt.Errorf("create temporary model catalog: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return CatalogSelection{}, fmt.Errorf("write temporary model catalog: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return CatalogSelection{}, fmt.Errorf("close temporary model catalog: %w", err)
	}
	closed = true
	return CatalogSelection{Model: name, Effort: effort}, nil
}

func selectReasoningEffort(model map[string]any, requested string) (string, error) {
	defaultEffort, _ := model["default_reasoning_level"].(string)
	levels, ok := model["supported_reasoning_levels"].([]any)
	if !ok || strings.TrimSpace(defaultEffort) == "" {
		return "", errors.New("selected model has incomplete reasoning metadata")
	}

	supported := make(map[string]struct{}, len(levels))
	for _, raw := range levels {
		level, ok := raw.(map[string]any)
		if !ok {
			return "", errors.New("selected model has invalid reasoning metadata")
		}
		effort, _ := level["effort"].(string)
		if strings.TrimSpace(effort) == "" {
			return "", errors.New("selected model has invalid reasoning metadata")
		}
		supported[effort] = struct{}{}
	}
	if _, ok := supported[defaultEffort]; !ok {
		return "", errors.New("selected model has invalid default reasoning effort")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return defaultEffort, nil
	}
	if _, ok := supported[requested]; !ok {
		return "", fmt.Errorf("reasoning effort %q is not available for the selected model", requested)
	}
	return requested, nil
}

func selectCatalogModel(models []map[string]any, requested string) (map[string]any, string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, model := range models {
			if slug, _ := model["slug"].(string); slug == requested {
				return cloneMap(model), slug, nil
			}
		}
		return nil, "", fmt.Errorf("model %q is not available in the Codex catalog", requested)
	}

	type candidate struct {
		priority float64
		slug     string
		model    map[string]any
	}
	var candidates []candidate
	for _, model := range models {
		slug, _ := model["slug"].(string)
		visibility, _ := model["visibility"].(string)
		priority, ok := model["priority"].(float64)
		if slug == "" || visibility != "list" || !ok {
			continue
		}
		candidates = append(candidates, candidate{priority: priority, slug: slug, model: model})
	}
	if len(candidates) == 0 {
		return nil, "", errors.New("Codex catalog has no visible default model")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].slug < candidates[j].slug
	})
	selected := candidates[0]
	return cloneMap(selected.model), selected.slug, nil
}

func removeAgentTools(model map[string]any) error {
	patchType, patchPresent := model["apply_patch_tool_type"]
	toolMode := model["tool_mode"]
	lite, litePresent := model["use_responses_lite"]
	if !patchPresent || !litePresent {
		return errors.New("required model metadata is missing")
	}
	if patchType != "freeform" {
		return errors.New("unexpected apply-patch mode")
	}
	if toolMode != nil && toolMode != "code_mode_only" {
		return errors.New("unexpected tool mode")
	}
	if _, ok := lite.(bool); !ok {
		return errors.New("unexpected responses mode")
	}
	model["apply_patch_tool_type"] = nil
	model["tool_mode"] = nil
	model["use_responses_lite"] = false
	return nil
}

func cloneMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func runBounded(ctx context.Context, command, args, env []string, limit int64) ([]byte, error) {
	fullArgs := append([]string{}, command[1:]...)
	fullArgs = append(fullArgs, args...)
	cmd := exec.CommandContext(ctx, command[0], fullArgs...)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open command output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Codex command: %w", err)
	}
	var output bytes.Buffer
	limited := &io.LimitedReader{R: stdout, N: limit + 1}
	if _, err := io.Copy(&output, limited); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("read Codex command output: %w", err)
	}
	if limited.N == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("Codex command output exceeded safety limit")
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("Codex command failed with exit code %d", exitErr.ExitCode())
		}
		return nil, errors.New("Codex command failed")
	}
	return output.Bytes(), nil
}

func isolatedEnv(base []string, codexHome, activeProfile string) []string {
	if base == nil {
		base = os.Environ()
	}
	env := codexstate.SanitizedEnv(base, strings.TrimSpace(codexHome))
	if activeProfile = strings.TrimSpace(activeProfile); activeProfile != "" {
		env = append(env, "MULTICODEX_ACTIVE_PROFILE="+activeProfile)
	}
	return env
}
