package codexappserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperModeEnv = "MULTICODEX_APP_SERVER_HELPER_MODE"

func TestGenerate(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       string
		wantOutput string
		wantError  string
		notInError string
		webSearch  bool
	}{
		{name: "success", mode: "success", wantOutput: "safe output"},
		{name: "search success", mode: "search", wantOutput: "safe output"},
		{name: "unexpected search rejected", mode: "unexpected-search", wantError: "unexpected item"},
		{name: "burst before response", mode: "burst-before-response", wantOutput: strings.Repeat("x", defaultNotificationSize+1)},
		{name: "api key rejected", mode: "api-key", wantError: "requires ChatGPT subscription"},
		{name: "unsafe effective config rejected", mode: "unsafe-effective-config", wantError: "safe generation configuration"},
		{name: "server request rejected", mode: "server-request", wantError: "unsupported action"},
		{name: "tool event rejected", mode: "tool-event", wantError: "commandexecution action"},
		{name: "non-search tool rejected during search", mode: "tool-event", webSearch: true, wantError: "commandexecution action"},
		{name: "RPC detail sanitized", mode: "rpc-error", wantError: "RPC code -32000", notInError: "private-secret"},
		{name: "turn RPC detail sanitized", mode: "turn-rpc-error", wantError: "RPC code -32002", notInError: "private-secret"},
		{name: "turn failure sanitized", mode: "turn-error", wantError: "generation failed", notInError: "private-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(helperModeEnv, test.mode)
			var output bytes.Buffer
			var writer io.Writer = &output
			if test.mode == "burst-before-response" {
				writer = delayedWriter{writer: &output, delay: time.Millisecond}
			}
			tempRoot := t.TempDir()
			err := Generate(t.Context(), GenerateOptions{
				Command:       helperCommand(t),
				BaseEnv:       []string{helperModeEnv + "=" + test.mode, "OPENAI_API_KEY=dummy", "CODEX_API_KEY=dummy", "PATH=" + os.Getenv("PATH")},
				CodexHome:     t.TempDir(),
				ActiveProfile: "synthetic-profile",
				Model:         "test-model",
				WebSearch:     test.mode == "search" || test.webSearch,
				Prompt:        "synthetic prompt",
				Output:        writer,
				TempRoot:      tempRoot,
			})
			entries, readErr := os.ReadDir(tempRoot)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("temporary generation state remains: entries=%d err=%v", len(entries), readErr)
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if output.String() != test.wantOutput {
					t.Fatalf("output = %q", output.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
			if test.notInError != "" && strings.Contains(err.Error(), test.notInError) {
				t.Fatalf("error exposed private detail: %v", err)
			}
		})
	}
}

func TestGenerateCustomInstructionsSchemaEffortAndJSON(t *testing.T) {
	t.Setenv(helperModeEnv, "custom-options")
	var output bytes.Buffer
	err := Generate(t.Context(), GenerateOptions{
		Command:               helperCommand(t),
		BaseEnv:               []string{helperModeEnv + "=custom-options", "PATH=" + os.Getenv("PATH")},
		CodexHome:             t.TempDir(),
		Model:                 "test-model",
		Effort:                "high",
		BaseInstructions:      "synthetic base instructions",
		DeveloperInstructions: "synthetic developer instructions",
		OutputSchema:          json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
		JSONOutput:            true,
		Prompt:                "synthetic prompt",
		Output:                &output,
		TempRoot:              t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result generationJSONResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON output: %v; output=%q", err, output.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]bool{
		"text": true, "model": true, "effort": true, "duration_ms": true,
		"web_search_calls": true, "usage": true,
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("JSON fields = %v", fields)
	}
	for field := range fields {
		if !wantFields[field] {
			t.Fatalf("unexpected JSON field %q", field)
		}
	}
	if result.Text != "safe output" || result.Model != "test-model" || result.Effort != "high" || result.DurationMS < 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Usage == nil || result.Usage.InputTokens != 80 || result.Usage.CachedInputTokens != 30 ||
		result.Usage.CacheWriteInputTokens != 5 || result.Usage.OutputTokens != 20 ||
		result.Usage.ReasoningOutputTokens != 7 || result.Usage.TotalTokens != 100 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	for _, forbidden := range []string{"synthetic-profile", "CodexHome", "ActiveProfile", "account"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("JSON output exposed %q: %s", forbidden, output.String())
		}
	}
}

func TestGenerateJSONReportsWebSearchCalls(t *testing.T) {
	for _, test := range []struct {
		mode      string
		webSearch bool
		want      int64
	}{
		{mode: "success", want: 0},
		{mode: "search", webSearch: true, want: 1},
		{mode: "search-twice", webSearch: true, want: 2},
		{mode: "search-completed-only", webSearch: true, want: 1},
		{mode: "search-duplicate-start", webSearch: true, want: 1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			t.Setenv(helperModeEnv, test.mode)
			var output bytes.Buffer
			err := Generate(t.Context(), GenerateOptions{
				Command:    helperCommand(t),
				BaseEnv:    []string{helperModeEnv + "=" + test.mode, "PATH=" + os.Getenv("PATH")},
				CodexHome:  t.TempDir(),
				Model:      "test-model",
				WebSearch:  test.webSearch,
				JSONOutput: true,
				Prompt:     "synthetic prompt",
				Output:     &output,
				TempRoot:   t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			var result generationJSONResult
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.WebSearchCalls != test.want {
				t.Fatalf("web search calls = %d, want %d", result.WebSearchCalls, test.want)
			}
		})
	}
}

func TestGenerateRejectsInvalidOrUnboundedWebSearchLifecycle(t *testing.T) {
	for _, mode := range []string{
		"search-without-id", "search-blank-id", "search-long-id",
		"search-too-many", "search-progress-only",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(helperModeEnv, mode)
			var output bytes.Buffer
			err := Generate(t.Context(), GenerateOptions{
				Command:    helperCommand(t),
				BaseEnv:    []string{helperModeEnv + "=" + mode, "PATH=" + os.Getenv("PATH")},
				CodexHome:  t.TempDir(),
				Model:      "test-model",
				WebSearch:  true,
				JSONOutput: true,
				Prompt:     "synthetic prompt",
				Output:     &output,
				TempRoot:   t.TempDir(),
			})
			if err == nil || output.Len() != 0 {
				t.Fatalf("Generate() error = %v, output = %q", err, output.String())
			}
			if strings.Contains(err.Error(), strings.Repeat("x", maxGenerationSearchIDBytes+1)) {
				t.Fatal("generation error exposed a rejected web-search identifier")
			}
		})
	}
}

func TestGenerateJSONFailureWritesNoOutput(t *testing.T) {
	t.Setenv(helperModeEnv, "turn-error")
	var output bytes.Buffer
	err := Generate(t.Context(), GenerateOptions{
		Command:    helperCommand(t),
		BaseEnv:    []string{helperModeEnv + "=turn-error", "PATH=" + os.Getenv("PATH")},
		CodexHome:  t.TempDir(),
		Model:      "test-model",
		JSONOutput: true,
		Prompt:     "synthetic prompt",
		Output:     &output,
		TempRoot:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "generation failed") {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("failed JSON generation wrote output: %q", output.String())
	}
}

func TestGenerateJSONTreatsMalformedUsageAsUnavailable(t *testing.T) {
	t.Setenv(helperModeEnv, "malformed-usage")
	var output bytes.Buffer
	err := Generate(t.Context(), GenerateOptions{
		Command:    helperCommand(t),
		BaseEnv:    []string{helperModeEnv + "=malformed-usage", "PATH=" + os.Getenv("PATH")},
		CodexHome:  t.TempDir(),
		Model:      "test-model",
		JSONOutput: true,
		Prompt:     "synthetic prompt",
		Output:     &output,
		TempRoot:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result generationJSONResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "safe output" || result.Usage != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestParseGenerationUsage(t *testing.T) {
	usage, err := parseGenerationUsage(json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":80,"cachedInputTokens":30,"cacheWriteInputTokens":5,"outputTokens":20,"reasoningOutputTokens":7,"totalTokens":100}}}`))
	if err != nil || usage.TotalTokens != 100 || usage.CachedInputTokens != 30 {
		t.Fatalf("usage = %#v, err = %v", usage, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":-1}}}`),
		json.RawMessage(`{}`),
		json.RawMessage(`{`),
	} {
		if _, err := parseGenerationUsage(raw); err == nil {
			t.Fatalf("invalid usage succeeded: %s", raw)
		}
	}
}

func TestBoundedGenerationBuffer(t *testing.T) {
	buffer := boundedGenerationBuffer{limit: 4}
	if count, err := buffer.Write([]byte("safe")); err != nil || count != 4 || buffer.String() != "safe" {
		t.Fatalf("bounded write: count=%d output=%q err=%v", count, buffer.String(), err)
	}
	if count, err := buffer.Write([]byte("x")); err == nil || count != 0 || buffer.String() != "safe" {
		t.Fatalf("overflow write: count=%d output=%q err=%v", count, buffer.String(), err)
	}
}

type delayedWriter struct {
	writer io.Writer
	delay  time.Duration
}

func (w delayedWriter) Write(data []byte) (int, error) {
	time.Sleep(w.delay)
	return w.writer.Write(data)
}

func TestGenerateRejectsEmptyPrompt(t *testing.T) {
	err := Generate(t.Context(), GenerateOptions{Prompt: " \n", Output: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestGenerateRejectsMissingCodexHome(t *testing.T) {
	err := Generate(t.Context(), GenerateOptions{Prompt: "synthetic prompt", Output: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "Codex home is not configured") {
		t.Fatalf("error = %v", err)
	}
}

func TestGenerateBoundsHandshakeNotificationPressure(t *testing.T) {
	t.Setenv(helperModeEnv, "handshake-burst")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := Generate(ctx, GenerateOptions{
		Command:   helperCommand(t),
		BaseEnv:   []string{helperModeEnv + "=handshake-burst", "PATH=" + os.Getenv("PATH")},
		CodexHome: t.TempDir(),
		Model:     "test-model",
		Prompt:    "synthetic prompt",
		Output:    &bytes.Buffer{},
		TempRoot:  t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("handshake error = %v, want bounded cancellation", err)
	}
}

func TestGenerationArgsEnforceSubscriptionIsolation(t *testing.T) {
	args := generationArgs("catalog.json", []string{"alpha.with.dot", "zeta"}, false)
	for _, override := range []string{
		`model_provider="openai"`,
		`chatgpt_base_url="https://chatgpt.com/backend-api/"`,
		`mcp_servers={"alpha.with.dot"={enabled=false},"zeta"={enabled=false}}`,
		`notify=[]`,
		`web_search="disabled"`,
	} {
		if !containsArg(args, override) {
			t.Fatalf("generation arguments do not contain %q", override)
		}
	}
	if args := generationArgs("catalog.json", nil, true); !containsArg(args, `web_search="live"`) {
		t.Fatal("search generation did not enable live web search")
	}
	toolFree := generationArgs("catalog.json", []string{"alpha.with.dot", "zeta"}, false)
	search := generationArgs("catalog.json", []string{"alpha.with.dot", "zeta"}, true)
	if len(toolFree) != len(search) {
		t.Fatal("search mode changed the generation argument count")
	}
	differences := 0
	for index := range toolFree {
		if toolFree[index] != search[index] {
			differences++
		}
	}
	if differences != 1 {
		t.Fatalf("search mode changed %d generation arguments, want one", differences)
	}
}

func TestGenerationConfigIsSafe(t *testing.T) {
	config := map[string]json.RawMessage{
		"model_provider":     json.RawMessage(`"openai"`),
		"model_catalog_json": json.RawMessage(`"catalog.json"`),
		"openai_base_url":    json.RawMessage(`null`),
		"chatgpt_base_url":   json.RawMessage(`"https://chatgpt.com/backend-api/"`),
		"debug":              json.RawMessage(`null`),
		"notify":             json.RawMessage(`[]`),
		"mcp_servers":        json.RawMessage(`{"safe":{"enabled":false}}`),
		"web_search":         json.RawMessage(`"disabled"`),
		"tools":              json.RawMessage(`{}`),
	}
	if !generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("safe configuration was rejected")
	}
	for _, name := range []string{"model_provider", "model_catalog_json", "chatgpt_base_url", "notify", "web_search"} {
		t.Run(name, func(t *testing.T) {
			changed := make(map[string]json.RawMessage, len(config))
			for key, value := range config {
				changed[key] = value
			}
			changed[name] = json.RawMessage(`null`)
			if generationConfigIsSafe(changed, "catalog.json", false) {
				t.Fatalf("unsafe %s configuration was accepted", name)
			}
		})
	}
	config["openai_base_url"] = json.RawMessage(`"https://example.invalid/v1"`)
	if generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("overridden OpenAI endpoint was accepted")
	}
	config["openai_base_url"] = json.RawMessage(`null`)
	config["debug"] = json.RawMessage(`{"config_lockfile":{"load_path":"/synthetic/config.lock.toml"}}`)
	if generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("effective config lockfile was accepted")
	}
	config["debug"] = json.RawMessage(`null`)
	config["mcp_servers"] = json.RawMessage(`{"unsafe":{"enabled":true}}`)
	if generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("enabled MCP server was accepted")
	}
	config["mcp_servers"] = json.RawMessage(`{"safe":{"enabled":false}}`)
	config["web_search"] = json.RawMessage(`"live"`)
	if !generationConfigIsSafe(config, "catalog.json", true) {
		t.Fatal("safe search configuration was rejected")
	}
	if generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("unexpected live web search was accepted")
	}
	config["tools"] = json.RawMessage(`{"web_search":{"allowed_domains":["example.com"],"search_context_size":"high","user_location":{"city":"Synthetic City","country":"GB","region":"Synthetic Region","timezone":"Etc/UTC"}}}`)
	if generationConfigIsSafe(config, "catalog.json", true) {
		t.Fatal("inherited web-search configuration was accepted")
	}
	config["tools"] = json.RawMessage(`{"web_search":{"allowed_domains":null,"search_context_size":null,"user_location":null}}`)
	if !generationConfigIsSafe(config, "catalog.json", true) {
		t.Fatal("null web-search defaults were rejected")
	}
	for _, tools := range []json.RawMessage{
		json.RawMessage(`{"web_search":true}`),
		json.RawMessage(`{"web_search":{"user_location":{"city":null,"country":null,"region":null,"timezone":null}}}`),
		json.RawMessage(`{"web_search":{"user_location":{}}}`),
	} {
		config["tools"] = tools
		if !generationConfigIsSafe(config, "catalog.json", true) {
			t.Fatalf("benign web-search configuration was rejected: %s", tools)
		}
	}
	config["tools"] = json.RawMessage(`{"web_search":{"unknown_setting":"value"}}`)
	if generationConfigIsSafe(config, "catalog.json", true) {
		t.Fatal("unknown non-null web-search setting was accepted")
	}
	config["web_search"] = json.RawMessage(`"disabled"`)
	if !generationConfigIsSafe(config, "catalog.json", false) {
		t.Fatal("tool-free generation rejected irrelevant nested search settings")
	}
}

func TestGenerationItemAllowed(t *testing.T) {
	for _, itemType := range []string{"agentMessage", "contextCompaction", "reasoning", "userMessage"} {
		if !generationItemAllowed(itemType, false) {
			t.Fatalf("safe item %q was rejected", itemType)
		}
	}
	if generationItemAllowed("commandExecution", true) {
		t.Fatal("tool item was accepted")
	}
	if generationItemAllowed("webSearch", false) || !generationItemAllowed("webSearch", true) {
		t.Fatal("web search item did not follow the explicit search boundary")
	}
}

func TestLiveGenerate(t *testing.T) {
	if os.Getenv("MULTICODEX_TEST_LIVE_GENERATE") != "1" {
		t.Skip("set MULTICODEX_TEST_LIVE_GENERATE=1 to use the active ChatGPT subscription")
	}
	for _, test := range []struct {
		name      string
		model     string
		webSearch bool
	}{
		{name: "default"},
		{name: "explicit model", model: "gpt-5.5"},
		{name: "search", webSearch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			var output bytes.Buffer
			err := Generate(ctx, GenerateOptions{
				CodexHome: os.Getenv("CODEX_HOME"),
				Model:     test.model,
				WebSearch: test.webSearch,
				Prompt:    "Reply with exactly OK and no other text.",
				Output:    &output,
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(output.String()) != "OK" {
				t.Fatal("live generation returned unexpected text")
			}
		})
	}
}

func TestCodexWireHasNoHarnessOrTools(t *testing.T) {
	testCodexWireGeneration(t, false)
}

func TestCodexWireHasOnlyWebSearch(t *testing.T) {
	testCodexWireGeneration(t, true)
}

func codexWireCommand(t *testing.T) []string {
	t.Helper()
	command := os.Getenv("MULTICODEX_TEST_CODEX_COMMAND")
	if command == "" {
		command = defaultCommand
	}
	output, err := exec.Command(command, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(output))
	if _, supported := supportedCodexVersions[version]; !supported {
		t.Fatalf("wire test Codex version %q is unsupported", version)
	}
	t.Logf("wire test runtime: %s", version)
	return []string{command}
}

func testCodexWireGeneration(t *testing.T, webSearch bool) {
	t.Helper()
	if os.Getenv("MULTICODEX_TEST_CODEX_WIRE") != "1" {
		t.Skip("set MULTICODEX_TEST_CODEX_WIRE=1 to inspect the installed Codex request locally")
	}
	requestBody := make(chan map[string]any, 1)
	mcpRequest := make(chan struct{}, 1)
	mcpServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case mcpRequest <- struct{}{}:
		default:
		}
	}))
	defer mcpServer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		requestBody <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		events := []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_test"}},
		}
		messageIndex := 0
		if webSearch {
			events = append(events,
				map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "web_search_call", "id": "search_test", "status": "in_progress"}},
				map[string]any{"type": "response.web_search_call.in_progress", "output_index": 0, "item_id": "search_test"},
				map[string]any{"type": "response.web_search_call.searching", "output_index": 0, "item_id": "search_test"},
				map[string]any{"type": "response.web_search_call.completed", "output_index": 0, "item_id": "search_test"},
				map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "web_search_call", "id": "search_test", "status": "completed", "action": map[string]any{"type": "search", "query": "synthetic-private-query", "sources": []map[string]any{{"type": "url", "url": "https://synthetic.invalid/private"}}}}},
			)
			messageIndex = 1
		}
		events = append(events,
			map[string]any{"type": "response.output_item.added", "output_index": messageIndex, "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_test", "status": "in_progress", "content": []any{}}},
			map[string]any{"type": "response.content_part.added", "item_id": "msg_test", "output_index": messageIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
			map[string]any{"type": "response.output_text.delta", "item_id": "msg_test", "output_index": messageIndex, "content_index": 0, "delta": "wire output"},
			map[string]any{"type": "response.output_text.done", "item_id": "msg_test", "output_index": messageIndex, "content_index": 0, "text": "wire output"},
			map[string]any{"type": "response.output_item.done", "output_index": messageIndex, "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_test", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "wire output", "annotations": []any{}}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_test"}},
		)
		for _, event := range events {
			data, _ := json.Marshal(event)
			fmt.Fprintf(writer, "data: %s\n\n", data)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := codexWireCommand(t)
	tempDir := t.TempDir()
	config := fmt.Sprintf("[mcp_servers.\"multicodex.test\"]\nurl = %s\n", strconv.Quote(mcpServer.URL))
	if err := os.WriteFile(filepath.Join(tempDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(tempDir, "catalog.json")
	selection, err := PrepareGenerationCatalog(ctx, CatalogOptions{Command: command, CodexHome: tempDir, WebSearch: webSearch, OutputPath: catalogPath})
	if err != nil {
		t.Fatal(err)
	}
	mcpServers, err := inspectGenerationConfig(tempDir, webSearch)
	if err != nil {
		t.Fatal(err)
	}
	args := generationArgs(catalogPath, mcpServers, webSearch)
	for _, override := range []string{
		`model_provider="multicodex_test"`,
		`model_providers.multicodex_test.name="Multicodex test"`,
		"model_providers.multicodex_test.base_url=" + strconv.Quote(server.URL+"/v1"),
		`model_providers.multicodex_test.env_key="MULTICODEX_SYNTHETIC_KEY"`,
		`model_providers.multicodex_test.wire_api="responses"`,
		"model_providers.multicodex_test.request_max_retries=0",
		"model_providers.multicodex_test.stream_max_retries=0",
	} {
		args = append(args, "-c", override)
	}
	client := New(Config{
		Command:        command,
		GlobalArgs:     args,
		BaseEnv:        []string{"PATH=" + os.Getenv("PATH"), "MULTICODEX_SYNTHETIC_KEY=synthetic"},
		CodexHome:      tempDir,
		ClientName:     "multicodex-wire-test",
		ClientVersion:  "test",
		CaptureEvents:  true,
		ErrorSanitizer: generationRPCError,
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(tempDir, "workspace")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var thread threadStartResult
	if err := client.Request(ctx, "thread/start", map[string]any{
		"model": selection.Model, "modelProvider": "multicodex_test", "cwd": workDir,
		"approvalPolicy": "never", "sandbox": "read-only",
		"baseInstructions": "synthetic base instructions", "developerInstructions": "synthetic developer instructions", "ephemeral": true,
	}, &thread); err != nil {
		t.Fatal(err)
	}
	var turn map[string]any
	if err := client.Request(ctx, "turn/start", map[string]any{
		"threadId": thread.Thread.ID,
		"input":    []map[string]any{{"type": "text", "text": "wire prompt"}},
		"effort":   "high",
		"outputSchema": map[string]any{
			"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}},
			"required": []string{"answer"}, "additionalProperties": false,
		},
	}, &turn); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result := streamGeneration(ctx, client, &output, webSearch)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if output.String() != "wire output" {
		t.Fatalf("output = %q", output.String())
	}
	wantSearchCalls := int64(0)
	if webSearch {
		wantSearchCalls = 1
	}
	if result.WebSearchCalls != wantSearchCalls {
		t.Fatalf("web search calls = %d, want %d", result.WebSearchCalls, wantSearchCalls)
	}
	for _, forbidden := range []string{"synthetic-private-query", "synthetic.invalid"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("wire output exposed %q", forbidden)
		}
	}

	var body map[string]any
	select {
	case body = <-requestBody:
	case <-ctx.Done():
		t.Fatal("Codex sent no provider request")
	}
	assertCustomWireRequest(t, body, webSearch)
	select {
	case <-mcpRequest:
		t.Fatal("Codex started a configured MCP server")
	default:
	}
}

func TestCodexRuntimePinsOpenAIProvider(t *testing.T) {
	if os.Getenv("MULTICODEX_TEST_CODEX_WIRE") != "1" {
		t.Skip("set MULTICODEX_TEST_CODEX_WIRE=1 to inspect the installed Codex runtime locally")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := codexWireCommand(t)
	tempDir := t.TempDir()
	config := `
model_provider = "multicodex_test"

[model_providers.multicodex_test]
name = "Multicodex test"
base_url = "https://example.invalid/v1"
env_key = "MULTICODEX_SYNTHETIC_KEY"
wire_api = "responses"
`
	if err := os.WriteFile(filepath.Join(tempDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(tempDir, "catalog.json")
	if _, err := PrepareGenerationCatalog(ctx, CatalogOptions{
		Command:    command,
		BaseEnv:    []string{"PATH=" + os.Getenv("PATH"), "MULTICODEX_SYNTHETIC_KEY=synthetic"},
		CodexHome:  tempDir,
		OutputPath: catalogPath,
	}); err != nil {
		t.Fatal(err)
	}
	client := New(Config{
		Command:       command,
		GlobalArgs:    generationArgs(catalogPath, nil, false),
		BaseEnv:       []string{"PATH=" + os.Getenv("PATH"), "MULTICODEX_SYNTHETIC_KEY=synthetic"},
		CodexHome:     tempDir,
		ClientName:    "multicodex-provider-test",
		ClientVersion: "test",
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var account accountReadResult
	if err := client.Request(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		t.Fatal(err)
	}
	if account.Account != nil {
		t.Fatalf("configured provider remained active: account type %q", account.Account.Type)
	}
	if !account.RequiresOpenAIAuth {
		t.Fatal("runtime did not require OpenAI authentication")
	}
}

func assertCustomWireRequest(t *testing.T, body map[string]any, webSearch bool) {
	t.Helper()
	if body["instructions"] != "synthetic base instructions" {
		t.Fatal("Codex did not send the exact base instructions")
	}
	tools, ok := body["tools"].([]any)
	wantTools := 0
	if webSearch {
		wantTools = 1
	}
	if !ok || len(tools) != wantTools {
		var kinds []string
		for _, value := range tools {
			tool, _ := value.(map[string]any)
			kinds = append(kinds, fmt.Sprintf("%v:%v", tool["type"], tool["name"]))
		}
		t.Fatalf("Codex sent tools: count=%d kinds=%v", len(tools), kinds)
	}
	if webSearch {
		tool, ok := tools[0].(map[string]any)
		if !ok || tool["type"] != "web_search" || tool["external_web_access"] != true || len(tool) != 3 {
			t.Fatalf("Codex sent a non-search tool: %#v", tools[0])
		}
		contentTypes, ok := tool["search_content_types"].([]any)
		if !ok || len(contentTypes) != 2 || contentTypes[0] != "text" || contentTypes[1] != "image" {
			t.Fatalf("Codex sent unexpected search content types: %#v", tool["search_content_types"])
		}
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("Codex sent %d input items, want developer and user messages", len(input))
	}
	developer, ok := input[0].(map[string]any)
	if !ok || developer["role"] != "developer" {
		t.Fatal("Codex did not send one developer message")
	}
	developerContent, ok := developer["content"].([]any)
	if !ok || len(developerContent) != 1 {
		t.Fatal("Codex changed the developer instructions")
	}
	developerText, ok := developerContent[0].(map[string]any)
	if !ok || developerText["type"] != "input_text" || developerText["text"] != "synthetic developer instructions" {
		t.Fatal("Codex changed the developer instructions")
	}
	message, ok := input[1].(map[string]any)
	if !ok || message["role"] != "user" {
		t.Fatal("Codex did not send one user message after the developer message")
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatal("Codex changed the user message content")
	}
	text, ok := content[0].(map[string]any)
	if !ok || text["type"] != "input_text" || text["text"] != "wire prompt" {
		t.Fatal("Codex changed the user prompt")
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatal("Codex did not send the requested reasoning effort")
	}
	textOptions, ok := body["text"].(map[string]any)
	format, formatOK := textOptions["format"].(map[string]any)
	schema, schemaOK := format["schema"].(map[string]any)
	if !ok || !formatOK || !schemaOK || format["type"] != "json_schema" || format["strict"] != true ||
		schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatal("Codex did not send the strict output schema")
	}
}

func TestAppServerHelper(t *testing.T) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		return
	}
	args := helperArgs(os.Args)
	switch {
	case len(args) == 1 && args[0] == "--version":
		if mode == "version-mismatch" {
			fmt.Println("private-version-detail")
			os.Exit(0)
		}
		if mode == "previous-version" {
			fmt.Println(PreviousSupportedCodexVersion)
			os.Exit(0)
		}
		fmt.Println(SupportedCodexVersion)
		os.Exit(0)
	case len(args) == 3 && args[0] == "debug" && args[1] == "models" && args[2] == "--bundled":
		if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("CODEX_API_KEY") != "" {
			os.Exit(20)
		}
		data, _ := json.Marshal(rawCatalog{Models: []map[string]any{compatibleTestModel("test-model", 1)}})
		fmt.Println(string(data))
		os.Exit(0)
	}
	if !containsArg(args, "app-server") || !containsArg(args, "-c") {
		os.Exit(21)
	}
	if !containsArg(args, "notify=[]") {
		os.Exit(23)
	}
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("CODEX_API_KEY") != "" {
		os.Exit(22)
	}
	runAppServerHelper(mode)
	os.Exit(0)
}

func helperCommand(t *testing.T) []string {
	t.Helper()
	return []string{os.Args[0], "-test.run=^TestAppServerHelper$", "--"}
}

func helperArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func runAppServerHelper(mode string) {
	reader := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for reader.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(reader.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		result := any(map[string]any{})
		switch req.Method {
		case "initialize":
		case "config/read":
			if mode == "handshake-burst" {
				for range defaultNotificationSize + 1 {
					_ = encoder.Encode(map[string]any{"method": "fs/changed", "params": map[string]any{}})
				}
			}
			config := map[string]any{
				"model_provider":     subscriptionProvider,
				"model_catalog_json": generationArgValue("model_catalog_json"),
				"openai_base_url":    nil,
				"chatgpt_base_url":   chatGPTBaseURL,
				"debug":              nil,
				"notify":             []string{},
				"mcp_servers":        map[string]any{},
				"web_search":         generationArgValue("web_search"),
			}
			if mode == "unsafe-effective-config" {
				config["chatgpt_base_url"] = "https://example.invalid/"
			}
			result = map[string]any{"config": config, "origins": map[string]any{}}
		case "account/read":
			if mode == "rpc-error" {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32000, "message": "private-secret"}})
				continue
			}
			accountType := "chatgpt"
			if mode == "api-key" {
				accountType = "apiKey"
			}
			result = map[string]any{"account": map[string]any{"type": accountType}, "requiresOpenaiAuth": true}
		case "thread/start":
			if !threadParamsAreExpected(req.Params, mode) {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32001, "message": "unsafe thread parameters"}})
				continue
			}
			result = map[string]any{"thread": map[string]any{"id": "thread-1"}}
		case "turn/start":
			if !turnParamsAreExpected(req.Params, mode) {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32002, "message": "unsafe turn parameters"}})
				continue
			}
			if mode == "turn-rpc-error" {
				_ = encoder.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": "partial"}})
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32002, "message": "private-secret"}})
				continue
			}
			if mode == "burst-before-response" {
				for range defaultNotificationSize + 1 {
					_ = encoder.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": "x"}})
				}
			}
			result = map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
		}
		_ = encoder.Encode(map[string]any{"id": req.ID, "result": result})
		if req.Method != "turn/start" {
			continue
		}
		switch mode {
		case "burst-before-response":
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
		case "server-request":
			_ = encoder.Encode(map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
		case "tool-event":
			_ = encoder.Encode(map[string]any{"method": "item/commandExecution/outputDelta", "params": map[string]any{}})
		case "turn-error":
			_ = encoder.Encode(map[string]any{"method": "error", "params": map[string]any{"message": "private-secret"}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "failed"}}})
		default:
			_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"type": "reasoning"}}})
			if mode == "search-without-id" {
				_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"type": "webSearch"}}})
			} else if mode == "search-blank-id" {
				_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"id": "   ", "type": "webSearch"}}})
			} else if mode == "search-long-id" {
				_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"id": strings.Repeat("x", maxGenerationSearchIDBytes+1), "type": "webSearch"}}})
			} else if mode == "search-too-many" {
				for index := range maxGenerationSearchItems + 1 {
					_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"id": fmt.Sprintf("search-%d", index), "type": "webSearch"}}})
				}
			} else if mode == "search-progress-only" {
				_ = encoder.Encode(map[string]any{"method": "item/webSearch/progress", "params": map[string]any{}})
			} else if mode == "search" || mode == "search-twice" || mode == "search-duplicate-start" || mode == "unexpected-search" {
				count := 1
				if mode == "search-twice" || mode == "search-duplicate-start" {
					count = 2
				}
				for index := range count {
					itemID := fmt.Sprintf("search-%d", index)
					if mode == "search-duplicate-start" {
						itemID = "search-0"
					}
					_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"id": itemID, "type": "webSearch"}}})
				}
				_ = encoder.Encode(map[string]any{"method": "item/webSearch/progress", "params": map[string]any{}})
				_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"id": "search-0", "type": "webSearch"}}})
			} else if mode == "search-completed-only" {
				_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"id": "search-0", "type": "webSearch"}}})
			}
			_ = encoder.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": "safe output"}})
			_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agentMessage"}}})
			if mode == "custom-options" {
				_ = encoder.Encode(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"tokenUsage": map[string]any{"total": map[string]any{
					"inputTokens": 80, "cachedInputTokens": 30, "cacheWriteInputTokens": 5,
					"outputTokens": 20, "reasoningOutputTokens": 7, "totalTokens": 100,
				}}}})
			} else if mode == "malformed-usage" {
				_ = encoder.Encode(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"tokenUsage": map[string]any{"total": map[string]any{"inputTokens": -1}}}})
			}
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
		}
	}
}

func generationArgValue(name string) string {
	args := helperArgs(os.Args)
	for index := 0; index+1 < len(args); index++ {
		if args[index] != "-c" {
			continue
		}
		key, value, ok := strings.Cut(args[index+1], "=")
		if ok && key == name {
			unquoted, err := strconv.Unquote(value)
			if err == nil {
				return unquoted
			}
		}
	}
	return ""
}

func threadParamsAreExpected(raw json.RawMessage, mode string) bool {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		return false
	}
	baseInstructions := ""
	developerInstructions := ""
	if mode == "custom-options" {
		baseInstructions = "synthetic base instructions"
		developerInstructions = "synthetic developer instructions"
	}
	return params["baseInstructions"] == baseInstructions &&
		params["developerInstructions"] == developerInstructions &&
		params["modelProvider"] == subscriptionProvider &&
		params["approvalPolicy"] == "never" &&
		params["sandbox"] == "read-only" &&
		params["ephemeral"] == true &&
		params["model"] == "test-model"
}

func turnParamsAreExpected(raw json.RawMessage, mode string) bool {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil || params["threadId"] != "thread-1" {
		return false
	}
	input, ok := params["input"].([]any)
	if !ok || len(input) != 1 {
		return false
	}
	message, ok := input[0].(map[string]any)
	if !ok || message["type"] != "text" || message["text"] != "synthetic prompt" {
		return false
	}
	if mode != "custom-options" {
		_, hasEffort := params["effort"]
		_, hasSchema := params["outputSchema"]
		return !hasEffort && !hasSchema
	}
	if params["effort"] != "high" {
		return false
	}
	schema, ok := params["outputSchema"].(map[string]any)
	return ok && schema["type"] == "object" && schema["additionalProperties"] == false
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}
