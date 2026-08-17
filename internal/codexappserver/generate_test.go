package codexappserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	}{
		{name: "success", mode: "success", wantOutput: "safe output"},
		{name: "api key rejected", mode: "api-key", wantError: "requires ChatGPT subscription"},
		{name: "server request rejected", mode: "server-request", wantError: "unsupported action"},
		{name: "tool event rejected", mode: "tool-event", wantError: "commandexecution action"},
		{name: "RPC detail sanitized", mode: "rpc-error", wantError: "RPC code -32000", notInError: "private-secret"},
		{name: "turn failure sanitized", mode: "turn-error", wantError: "generation failed", notInError: "private-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(helperModeEnv, test.mode)
			var output bytes.Buffer
			tempRoot := t.TempDir()
			err := Generate(t.Context(), GenerateOptions{
				Command:       helperCommand(t),
				BaseEnv:       []string{helperModeEnv + "=" + test.mode, "OPENAI_API_KEY=dummy", "CODEX_API_KEY=dummy", "PATH=" + os.Getenv("PATH")},
				CodexHome:     t.TempDir(),
				ActiveProfile: "synthetic-profile",
				Model:         "test-model",
				Prompt:        "synthetic prompt",
				Output:        &output,
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

func TestGenerateRejectsEmptyPrompt(t *testing.T) {
	err := Generate(t.Context(), GenerateOptions{Prompt: " \n", Output: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestLiveGenerate(t *testing.T) {
	if os.Getenv("MULTICODEX_TEST_LIVE_GENERATE") != "1" {
		t.Skip("set MULTICODEX_TEST_LIVE_GENERATE=1 to use the active ChatGPT subscription")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var output bytes.Buffer
	err := Generate(ctx, GenerateOptions{
		CodexHome: os.Getenv("CODEX_HOME"),
		Prompt:    "Reply with exactly OK and no other text.",
		Output:    &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "OK" {
		t.Fatal("live generation returned unexpected text")
	}
}

func TestCodexWireHasNoHarnessOrTools(t *testing.T) {
	if os.Getenv("MULTICODEX_TEST_CODEX_WIRE") != "1" {
		t.Skip("set MULTICODEX_TEST_CODEX_WIRE=1 to inspect the installed Codex request locally")
	}
	requestBody := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		requestBody <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_test"}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_test", "status": "in_progress", "content": []any{}}},
			{"type": "response.content_part.added", "item_id": "msg_test", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
			{"type": "response.output_text.delta", "item_id": "msg_test", "output_index": 0, "content_index": 0, "delta": "wire output"},
			{"type": "response.output_text.done", "item_id": "msg_test", "output_index": 0, "content_index": 0, "text": "wire output"},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_test", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "wire output", "annotations": []any{}}}}},
			{"type": "response.completed", "response": map[string]any{"id": "resp_test"}},
		} {
			data, _ := json.Marshal(event)
			fmt.Fprintf(writer, "data: %s\n\n", data)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tempDir := t.TempDir()
	catalogPath := filepath.Join(tempDir, "catalog.json")
	model, err := PrepareToolFreeCatalog(ctx, CatalogOptions{CodexHome: tempDir, OutputPath: catalogPath})
	if err != nil {
		t.Fatal(err)
	}
	args := generationArgs(catalogPath)
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
		"model": model, "modelProvider": "multicodex_test", "cwd": workDir,
		"approvalPolicy": "never", "sandbox": "read-only",
		"baseInstructions": "", "developerInstructions": "", "ephemeral": true,
	}, &thread); err != nil {
		t.Fatal(err)
	}
	var turn map[string]any
	if err := client.Request(ctx, "turn/start", map[string]any{
		"threadId": thread.Thread.ID,
		"input":    []map[string]any{{"type": "text", "text": "wire prompt"}},
	}, &turn); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := streamGeneration(ctx, client, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "wire output" {
		t.Fatalf("output = %q", output.String())
	}

	var body map[string]any
	select {
	case body = <-requestBody:
	case <-ctx.Done():
		t.Fatal("Codex sent no provider request")
	}
	assertToolFreeWireRequest(t, body)
}

func assertToolFreeWireRequest(t *testing.T, body map[string]any) {
	t.Helper()
	if instructions, exists := body["instructions"]; exists && instructions != nil && instructions != "" {
		t.Fatal("Codex sent non-empty instructions")
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("Codex sent tools: count=%d", len(tools))
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("Codex sent %d input items, want one", len(input))
	}
	message, ok := input[0].(map[string]any)
	if !ok || message["role"] != "user" {
		t.Fatal("Codex did not send one user message")
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatal("Codex changed the user message content")
	}
	text, ok := content[0].(map[string]any)
	if !ok || text["type"] != "input_text" || text["text"] != "wire prompt" {
		t.Fatal("Codex changed the user prompt")
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
		fmt.Println(SupportedCodexVersion)
		os.Exit(0)
	case len(args) == 2 && args[0] == "debug" && args[1] == "models":
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
			if !threadParamsAreToolFree(req.Params) {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32001, "message": "unsafe thread parameters"}})
				continue
			}
			result = map[string]any{"thread": map[string]any{"id": "thread-1"}}
		case "turn/start":
			if !turnParamsAreExpected(req.Params) {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32002, "message": "unsafe turn parameters"}})
				continue
			}
			result = map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
		}
		_ = encoder.Encode(map[string]any{"id": req.ID, "result": result})
		if req.Method != "turn/start" {
			continue
		}
		switch mode {
		case "server-request":
			_ = encoder.Encode(map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
		case "tool-event":
			_ = encoder.Encode(map[string]any{"method": "item/commandExecution/outputDelta", "params": map[string]any{}})
		case "turn-error":
			_ = encoder.Encode(map[string]any{"method": "error", "params": map[string]any{"message": "private-secret"}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "failed"}}})
		default:
			_ = encoder.Encode(map[string]any{"method": "item/started", "params": map[string]any{"item": map[string]any{"type": "reasoning"}}})
			_ = encoder.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": "safe output"}})
			_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agentMessage"}}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
		}
	}
}

func threadParamsAreToolFree(raw json.RawMessage) bool {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		return false
	}
	return params["baseInstructions"] == "" &&
		params["developerInstructions"] == "" &&
		params["approvalPolicy"] == "never" &&
		params["sandbox"] == "read-only" &&
		params["ephemeral"] == true &&
		params["model"] == "test-model"
}

func turnParamsAreExpected(raw json.RawMessage) bool {
	var params struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	return json.Unmarshal(raw, &params) == nil && params.ThreadID == "thread-1" && len(params.Input) == 1 && params.Input[0].Type == "text" && params.Input[0].Text == "synthetic prompt"
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}
