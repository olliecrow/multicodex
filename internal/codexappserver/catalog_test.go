package codexappserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectCatalogModel(t *testing.T) {
	models := []map[string]any{
		{"slug": "second", "visibility": "list", "priority": float64(2)},
		{"slug": "hidden", "visibility": "hide", "priority": float64(0)},
		{"slug": "first", "visibility": "list", "priority": float64(1)},
	}

	selected, name, err := selectCatalogModel(models, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "first" || selected["slug"] != "first" {
		t.Fatalf("selected %q: %#v", name, selected)
	}
	selected["slug"] = "changed"
	if models[2]["slug"] != "first" {
		t.Fatal("selection mutated the source model")
	}

	_, name, err = selectCatalogModel(models, "second")
	if err != nil || name != "second" {
		t.Fatalf("requested selection = %q, %v", name, err)
	}
	if _, _, err := selectCatalogModel(models, "missing"); err == nil {
		t.Fatal("missing requested model succeeded")
	}
}

func TestRemoveAgentTools(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "code mode", mutate: func(map[string]any) {}},
		{name: "null tool mode", mutate: func(model map[string]any) { model["tool_mode"] = nil }},
		{name: "omitted tool mode", mutate: func(model map[string]any) { delete(model, "tool_mode") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := compatibleTestModel("test-model", 1)
			test.mutate(model)
			if err := removeAgentTools(model); err != nil {
				t.Fatal(err)
			}
			if model["apply_patch_tool_type"] != nil || model["tool_mode"] != nil || model["use_responses_lite"] != false {
				t.Fatalf("tool fields were not disabled: %#v", model)
			}
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"missing patch metadata":     func(model map[string]any) { delete(model, "apply_patch_tool_type") },
		"missing responses metadata": func(model map[string]any) { delete(model, "use_responses_lite") },
		"unknown patch":              func(model map[string]any) { model["apply_patch_tool_type"] = "secret-value" },
		"unknown mode":               func(model map[string]any) { model["tool_mode"] = "secret-value" },
		"invalid lite":               func(model map[string]any) { model["use_responses_lite"] = "secret-value" },
	} {
		t.Run(name, func(t *testing.T) {
			model := compatibleTestModel("test-model", 1)
			mutate(model)
			err := removeAgentTools(model)
			if err == nil {
				t.Fatal("incompatible metadata succeeded")
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error exposed catalog data: %v", err)
			}
		})
	}
}

func TestSelectReasoningEffort(t *testing.T) {
	model := compatibleTestModel("test-model", 1)
	for _, test := range []struct {
		name      string
		requested string
		want      string
		wantError string
	}{
		{name: "default", want: "medium"},
		{name: "explicit", requested: "high", want: "high"},
		{name: "trimmed", requested: " low ", want: "low"},
		{name: "unsupported", requested: "ultra", wantError: "not available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectReasoningEffort(model, test.requested)
			if test.wantError == "" {
				if err != nil || got != test.want {
					t.Fatalf("effort = %q, err = %v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"missing default": func(candidate map[string]any) { delete(candidate, "default_reasoning_level") },
		"missing levels":  func(candidate map[string]any) { delete(candidate, "supported_reasoning_levels") },
		"invalid level": func(candidate map[string]any) {
			candidate["supported_reasoning_levels"] = []any{"private-catalog-value"}
		},
		"unsupported default": func(candidate map[string]any) { candidate["default_reasoning_level"] = "private-catalog-value" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := compatibleTestModel("test-model", 1)
			mutate(candidate)
			_, err := selectReasoningEffort(candidate, "")
			if err == nil {
				t.Fatal("invalid reasoning metadata succeeded")
			}
			if strings.Contains(err.Error(), "private-catalog-value") {
				t.Fatalf("error exposed catalog data: %v", err)
			}
		})
	}
}

func TestPrepareGenerationCatalog(t *testing.T) {
	t.Setenv(helperModeEnv, "success")
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	model, err := PrepareGenerationCatalog(t.Context(), CatalogOptions{
		Command:         helperCommand(t),
		BaseEnv:         []string{helperModeEnv + "=success", "OPENAI_API_KEY=dummy", "PATH=" + os.Getenv("PATH")},
		CodexHome:       filepath.Join(dir, "codex-home"),
		ActiveProfile:   "synthetic-profile",
		RequestedModel:  "test-model",
		RequestedEffort: "high",
		OutputPath:      path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.Model != "test-model" || model.Effort != "high" {
		t.Fatalf("selection = %#v", model)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog rawCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("model count = %d", len(catalog.Models))
	}
	selected := catalog.Models[0]
	if selected["apply_patch_tool_type"] != nil || selected["tool_mode"] != nil || selected["use_responses_lite"] != false || selected["supports_search_tool"] != false {
		t.Fatalf("unsafe catalog: %#v", selected)
	}

	searchPath := filepath.Join(dir, "search-catalog.json")
	if _, err := PrepareGenerationCatalog(t.Context(), CatalogOptions{
		Command: helperCommand(t), BaseEnv: []string{helperModeEnv + "=success", "PATH=" + os.Getenv("PATH")},
		RequestedModel: "test-model", WebSearch: true, OutputPath: searchPath,
	}); err != nil {
		t.Fatal(err)
	}
	searchData, err := os.ReadFile(searchPath)
	if err != nil {
		t.Fatal(err)
	}
	var searchCatalog rawCatalog
	if err := json.Unmarshal(searchData, &searchCatalog); err != nil || searchCatalog.Models[0]["supports_search_tool"] != true {
		t.Fatalf("search catalog metadata is invalid: err=%v", err)
	}

	if _, err := PrepareGenerationCatalog(t.Context(), CatalogOptions{
		Command:    helperCommand(t),
		BaseEnv:    []string{helperModeEnv + "=success", "PATH=" + os.Getenv("PATH")},
		OutputPath: path,
	}); err == nil {
		t.Fatal("existing catalog path was overwritten")
	}
}

func TestConfigureSearchMetadata(t *testing.T) {
	for _, test := range []struct {
		name      string
		metadata  any
		present   bool
		webSearch bool
		want      any
		wantError bool
	}{
		{name: "search supported", metadata: true, present: true, webSearch: true, want: true},
		{name: "search unsupported", metadata: false, present: true, webSearch: true, wantError: true},
		{name: "search metadata missing", webSearch: true, wantError: true},
		{name: "default disables support", metadata: true, present: true, want: false},
		{name: "default permits missing metadata"},
		{name: "default rejects malformed metadata", metadata: "private-value", present: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := map[string]any{}
			if test.present {
				model["supports_search_tool"] = test.metadata
			}
			err := configureSearchMetadata(model, test.webSearch)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-value") {
				t.Fatalf("error exposed catalog data: %v", err)
			}
			if !test.wantError && test.present && model["supports_search_tool"] != test.want {
				t.Fatalf("supports_search_tool = %#v, want %#v", model["supports_search_tool"], test.want)
			}
		})
	}
}

func TestPrepareGenerationCatalogRejectsOtherCodexVersionSafely(t *testing.T) {
	t.Setenv(helperModeEnv, "version-mismatch")
	_, err := PrepareGenerationCatalog(t.Context(), CatalogOptions{
		Command:    helperCommand(t),
		BaseEnv:    []string{helperModeEnv + "=version-mismatch", "PATH=" + os.Getenv("PATH")},
		OutputPath: filepath.Join(t.TempDir(), "catalog.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported Codex version") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "private-version-detail") {
		t.Fatalf("error exposed subprocess output: %v", err)
	}
}

func TestSupportedCodexVersions(t *testing.T) {
	want := []string{"codex-cli 0.147.0", "codex-cli 0.148.0"}
	if PreviousSupportedCodexVersion != want[0] || SupportedCodexVersion != want[1] {
		t.Fatalf("version constants = %q, %q; want %q, %q", PreviousSupportedCodexVersion, SupportedCodexVersion, want[0], want[1])
	}
	if len(supportedCodexVersions) != len(want) {
		t.Fatalf("supported version count = %d; want %d", len(supportedCodexVersions), len(want))
	}
	for _, version := range want {
		if _, ok := supportedCodexVersions[version]; !ok {
			t.Fatalf("supported version %q is missing", version)
		}
	}
	if _, ok := supportedCodexVersions["codex-cli 0.149.0"]; ok {
		t.Fatal("untested Codex version was accepted")
	}
}

func TestPrepareGenerationCatalogSupportsPreviousCodexVersion(t *testing.T) {
	t.Setenv(helperModeEnv, "previous-version")
	_, err := PrepareGenerationCatalog(t.Context(), CatalogOptions{
		Command:    helperCommand(t),
		BaseEnv:    []string{helperModeEnv + "=previous-version", "PATH=" + os.Getenv("PATH")},
		OutputPath: filepath.Join(t.TempDir(), "catalog.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func compatibleTestModel(slug string, priority int) map[string]any {
	return map[string]any{
		"slug":                    slug,
		"visibility":              "list",
		"priority":                float64(priority),
		"apply_patch_tool_type":   "freeform",
		"tool_mode":               "code_mode_only",
		"use_responses_lite":      true,
		"supports_search_tool":    true,
		"web_search_tool_type":    "text_and_image",
		"default_reasoning_level": "medium",
		"supported_reasoning_levels": []any{
			map[string]any{"effort": "low"},
			map[string]any{"effort": "medium"},
			map[string]any{"effort": "high"},
		},
	}
}
