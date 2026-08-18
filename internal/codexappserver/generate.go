package codexappserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/olliecrow/multicodex/internal/buildinfo"
)

const (
	subscriptionProvider       = "openai"
	chatGPTBaseURL             = "https://chatgpt.com/backend-api/"
	generationHandshakeTimeout = 30 * time.Second
	maxBufferedResponseBytes   = 16 * 1024 * 1024
)

type GenerateOptions struct {
	Command               []string
	BaseEnv               []string
	CodexHome             string
	ActiveProfile         string
	Model                 string
	Effort                string
	BaseInstructions      string
	DeveloperInstructions string
	OutputSchema          json.RawMessage
	JSONOutput            bool
	Prompt                string
	Output                io.Writer
	TempRoot              string
}

type GenerationUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type generationJSONResult struct {
	Text       string           `json:"text"`
	Model      string           `json:"model"`
	Effort     string           `json:"effort"`
	DurationMS int64            `json:"duration_ms"`
	Usage      *GenerationUsage `json:"usage"`
}

type generationStreamResult struct {
	Usage *GenerationUsage
	Err   error
}

type boundedGenerationBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedGenerationBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		return 0, errors.New("buffered generation response exceeded safety limit")
	}
	return b.buffer.Write(data)
}

func (b *boundedGenerationBuffer) String() string {
	return b.buffer.String()
}

type accountReadResult struct {
	Account *struct {
		Type string `json:"type"`
	} `json:"account"`
	RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
}

type threadStartResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnCompletedParams struct {
	Turn struct {
		Status string `json:"status"`
	} `json:"turn"`
}

type deltaParams struct {
	Delta string `json:"delta"`
}

type itemParams struct {
	Item struct {
		Type string `json:"type"`
	} `json:"item"`
}

type configReadResult struct {
	Config map[string]json.RawMessage `json:"config"`
}

type tokenUsageUpdatedParams struct {
	TokenUsage struct {
		Total *struct {
			InputTokens           int64 `json:"inputTokens"`
			CachedInputTokens     int64 `json:"cachedInputTokens"`
			CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
			OutputTokens          int64 `json:"outputTokens"`
			ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
			TotalTokens           int64 `json:"totalTokens"`
		} `json:"total"`
	} `json:"tokenUsage"`
}

func Generate(ctx context.Context, options GenerateOptions) error {
	started := time.Now()
	if strings.TrimSpace(options.Prompt) == "" {
		return errors.New("prompt is empty")
	}
	if options.Output == nil {
		return errors.New("generation output is not configured")
	}
	if strings.TrimSpace(options.CodexHome) == "" {
		return errors.New("generation Codex home is not configured")
	}
	mcpServers, err := inspectGenerationConfig(options.CodexHome)
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp(options.TempRoot, "multicodex-generate-")
	if err != nil {
		return fmt.Errorf("create generation workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	workDir := filepath.Join(tempDir, "empty-workspace")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return fmt.Errorf("create empty generation workspace: %w", err)
	}
	catalogPath := filepath.Join(tempDir, "model-catalog.json")
	selection, err := PrepareToolFreeCatalog(ctx, CatalogOptions{
		Command:         options.Command,
		BaseEnv:         options.BaseEnv,
		CodexHome:       options.CodexHome,
		ActiveProfile:   options.ActiveProfile,
		RequestedModel:  options.Model,
		RequestedEffort: options.Effort,
		OutputPath:      catalogPath,
	})
	if err != nil {
		return err
	}

	client := New(Config{
		Command:        options.Command,
		GlobalArgs:     generationArgs(catalogPath, mcpServers),
		BaseEnv:        options.BaseEnv,
		CodexHome:      options.CodexHome,
		ActiveProfile:  options.ActiveProfile,
		ClientName:     "multicodex-generate",
		ClientVersion:  buildinfo.Version,
		CaptureEvents:  true,
		ErrorSanitizer: generationRPCError,
	})
	if err := client.Start(); err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, generationHandshakeTimeout)
	defer cancelHandshake()
	if err := client.Initialize(handshakeCtx); err != nil {
		return fmt.Errorf("initialize generation runtime: %w", err)
	}
	var runtimeConfig configReadResult
	if err := client.Request(handshakeCtx, "config/read", map[string]any{
		"includeLayers": false,
		"cwd":           workDir,
	}, &runtimeConfig); err != nil {
		return fmt.Errorf("check generation configuration: %w", err)
	}
	if !generationConfigIsSafe(runtimeConfig.Config, catalogPath) {
		return errors.New("Codex did not apply the required safe generation configuration")
	}

	var account accountReadResult
	if err := client.Request(handshakeCtx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return fmt.Errorf("check subscription account: %w", err)
	}
	if account.Account == nil {
		if account.RequiresOpenAIAuth {
			return errors.New("ChatGPT subscription sign-in is required")
		}
		return errors.New("ChatGPT subscription account is unavailable")
	}
	if account.Account.Type != "chatgpt" {
		return errors.New("generate requires ChatGPT subscription authentication; API-key billing is not used")
	}

	var thread threadStartResult
	if err := client.Request(handshakeCtx, "thread/start", map[string]any{
		"model":                 selection.Model,
		"modelProvider":         subscriptionProvider,
		"cwd":                   workDir,
		"approvalPolicy":        "never",
		"sandbox":               "read-only",
		"baseInstructions":      options.BaseInstructions,
		"developerInstructions": options.DeveloperInstructions,
		"ephemeral":             true,
		"serviceName":           "multicodex_generate",
	}, &thread); err != nil {
		return fmt.Errorf("start generation thread: %w", err)
	}
	if strings.TrimSpace(thread.Thread.ID) == "" {
		return errors.New("start generation thread: app-server returned no thread id")
	}
	cancelHandshake()
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	streamOutput := options.Output
	bufferedOutput := boundedGenerationBuffer{limit: maxBufferedResponseBytes}
	if options.JSONOutput {
		streamOutput = &bufferedOutput
	}
	streamResult := make(chan generationStreamResult, 1)
	go func() {
		streamResult <- streamGeneration(streamCtx, client, streamOutput)
	}()
	turnResult := make(chan error, 1)
	go func() {
		var turn map[string]any
		params := map[string]any{
			"threadId": thread.Thread.ID,
			"input": []map[string]any{{
				"type": "text",
				"text": options.Prompt,
			}},
		}
		if strings.TrimSpace(options.Effort) != "" {
			params["effort"] = selection.Effort
		}
		if len(options.OutputSchema) != 0 {
			params["outputSchema"] = options.OutputSchema
		}
		turnResult <- client.Request(ctx, "turn/start", params, &turn)
	}()

	turnDone := false
	streamDone := false
	var completed generationStreamResult
	for !turnDone || !streamDone {
		select {
		case err := <-turnResult:
			turnDone = true
			if err != nil {
				cancelStream()
				if !streamDone {
					<-streamResult
				}
				return fmt.Errorf("start generation turn: %w", err)
			}
		case result := <-streamResult:
			streamDone = true
			completed = result
			if result.Err != nil {
				return result.Err
			}
		}
	}
	if options.JSONOutput {
		result := generationJSONResult{
			Text:       bufferedOutput.String(),
			Model:      selection.Model,
			Effort:     selection.Effort,
			DurationMS: time.Since(started).Milliseconds(),
			Usage:      completed.Usage,
		}
		if err := json.NewEncoder(options.Output).Encode(result); err != nil {
			return fmt.Errorf("write generation output: %w", err)
		}
	}
	return nil
}

func streamGeneration(ctx context.Context, client *Client, output io.Writer) generationStreamResult {
	failure := errors.New("generation failed")
	var usage *GenerationUsage
	for {
		if err := ctx.Err(); err != nil {
			return generationStreamResult{Err: fmt.Errorf("generation canceled: %w", err)}
		}
		select {
		case <-ctx.Done():
			return generationStreamResult{Err: fmt.Errorf("generation canceled: %w", ctx.Err())}
		case <-client.Done():
			return generationStreamResult{Err: fmt.Errorf("generation runtime stopped: %w", client.Err())}
		case event, ok := <-client.Events():
			if !ok {
				return generationStreamResult{Err: fmt.Errorf("generation runtime stopped: %w", client.Err())}
			}
			if event.ServerRequest {
				return generationStreamResult{Err: errors.New("generation stopped because the runtime requested an unsupported action")}
			}
			if category := toolEventCategory(event); category != "" {
				return generationStreamResult{Err: fmt.Errorf("generation stopped because the runtime exposed a %s action", category)}
			}
			switch event.Method {
			case "item/agentMessage/delta":
				var params deltaParams
				if err := json.Unmarshal(event.Params, &params); err != nil {
					return generationStreamResult{Err: errors.New("decode generation text event")}
				}
				if _, err := io.WriteString(output, params.Delta); err != nil {
					return generationStreamResult{Err: fmt.Errorf("write generation output: %w", err)}
				}
			case "item/started", "item/completed":
				var params itemParams
				if err := json.Unmarshal(event.Params, &params); err != nil {
					return generationStreamResult{Err: errors.New("decode generation item event")}
				}
				if !generationItemAllowed(params.Item.Type) {
					return generationStreamResult{Err: errors.New("generation stopped because the runtime exposed an unexpected item")}
				}
			case "thread/tokenUsage/updated":
				parsed, err := parseGenerationUsage(event.Params)
				if err == nil {
					usage = &parsed
				}
			case "error":
				failure = classifyGenerationFailure(event.Params)
			case "turn/completed":
				var params turnCompletedParams
				if err := json.Unmarshal(event.Params, &params); err != nil {
					return generationStreamResult{Err: errors.New("decode generation completion event")}
				}
				if params.Turn.Status != "completed" {
					return generationStreamResult{Err: failure}
				}
				return generationStreamResult{Usage: usage}
			}
		}
	}
}

func parseGenerationUsage(raw json.RawMessage) (GenerationUsage, error) {
	var params tokenUsageUpdatedParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return GenerationUsage{}, errors.New("decode generation usage event")
	}
	if params.TokenUsage.Total == nil {
		return GenerationUsage{}, errors.New("decode generation usage event")
	}
	total := params.TokenUsage.Total
	usage := GenerationUsage{
		InputTokens:           total.InputTokens,
		CachedInputTokens:     total.CachedInputTokens,
		CacheWriteInputTokens: total.CacheWriteInputTokens,
		OutputTokens:          total.OutputTokens,
		ReasoningOutputTokens: total.ReasoningOutputTokens,
		TotalTokens:           total.TotalTokens,
	}
	if usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.CacheWriteInputTokens < 0 ||
		usage.OutputTokens < 0 || usage.ReasoningOutputTokens < 0 || usage.TotalTokens < 0 {
		return GenerationUsage{}, errors.New("decode generation usage event")
	}
	return usage, nil
}

func generationArgs(catalogPath string, mcpServers []string) []string {
	overrides := []string{
		"model_catalog_json=" + strconv.Quote(catalogPath),
		`model_provider="` + subscriptionProvider + `"`,
		"chatgpt_base_url=" + strconv.Quote(chatGPTBaseURL),
		disabledMCPServersOverride(mcpServers),
		"notify=[]",
		"include_permissions_instructions=false",
		"include_apps_instructions=false",
		"include_collaboration_mode_instructions=false",
		"include_environment_context=false",
		"skills.include_instructions=false",
		"skills.bundled.enabled=false",
		"orchestrator.skills.enabled=false",
		"orchestrator.mcp.enabled=false",
		"project_doc_max_bytes=0",
		"agents.enabled=false",
		"tools.update_plan.enabled=false",
		"tools.experimental_request_user_input.enabled=false",
		`web_search="disabled"`,
		"features.view_image=false",
		"features.shell_tool=false",
		"features.unified_exec=false",
		"features.multi_agent=false",
		"features.multi_agent_v2=false",
		"features.apps=false",
		"features.plugins=false",
		"features.browser_use=false",
		"features.computer_use=false",
		"features.image_generation=false",
		"features.goals=false",
		"features.hooks=false",
		"features.tool_suggest=false",
		"features.tool_search=false",
		"features.code_mode=false",
		"features.code_mode_only=false",
		"features.apply_patch_freeform=false",
	}
	args := []string{"-s", "read-only", "-a", "never"}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	return args
}

func generationConfigIsSafe(config map[string]json.RawMessage, catalogPath string) bool {
	if !configStringEquals(config, "model_provider", subscriptionProvider) ||
		!configStringEquals(config, "model_catalog_json", catalogPath) ||
		!configStringUnset(config, "openai_base_url") ||
		!configStringEquals(config, "chatgpt_base_url", chatGPTBaseURL) ||
		!configLockUnset(config) {
		return false
	}
	var notify *[]string
	if raw, ok := config["notify"]; !ok || json.Unmarshal(raw, &notify) != nil || notify == nil || len(*notify) != 0 {
		return false
	}
	var servers map[string]struct {
		Enabled *bool `json:"enabled"`
	}
	if raw, ok := config["mcp_servers"]; ok {
		if json.Unmarshal(raw, &servers) != nil {
			return false
		}
		for _, server := range servers {
			if server.Enabled == nil || *server.Enabled {
				return false
			}
		}
	}
	return true
}

func configStringEquals(config map[string]json.RawMessage, name, expected string) bool {
	var value string
	raw, ok := config[name]
	return ok && json.Unmarshal(raw, &value) == nil && value == expected
}

func configStringUnset(config map[string]json.RawMessage, name string) bool {
	raw, ok := config[name]
	return !ok || string(raw) == "null"
}

func configLockUnset(config map[string]json.RawMessage) bool {
	raw, ok := config["debug"]
	if !ok || string(raw) == "null" {
		return true
	}
	var debug struct {
		ConfigLockfile *struct {
			LoadPath *string `json:"load_path"`
		} `json:"config_lockfile"`
	}
	return json.Unmarshal(raw, &debug) == nil &&
		(debug.ConfigLockfile == nil || debug.ConfigLockfile.LoadPath == nil)
}

func disabledMCPServersOverride(names []string) string {
	servers := make([]string, 0, len(names))
	for _, name := range names {
		servers = append(servers, strconv.QuoteToASCII(name)+"={enabled=false}")
	}
	return "mcp_servers={" + strings.Join(servers, ",") + "}"
}

func generationRPCError(method string, code int, message string) error {
	lower := strings.ToLower(message)
	detail := ""
	switch {
	case strings.Contains(lower, "token_expired"), strings.Contains(lower, "authentication token is expired"):
		detail = ": authentication expired; sign in again"
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid token"):
		detail = ": authentication rejected; sign in again"
	case strings.Contains(lower, "usage limit"), strings.Contains(lower, "usage_limit"):
		detail = ": subscription usage limit reached"
	}
	return fmt.Errorf("app-server %s failed with RPC code %d%s", method, code, detail)
}

func toolEventCategory(event Event) string {
	method := strings.ToLower(event.Method)
	for _, marker := range []string{"commandexecution", "filechange", "websearch", "imagegeneration"} {
		if strings.Contains(method, marker) {
			return marker
		}
	}
	return ""
}

func generationItemAllowed(itemType string) bool {
	switch itemType {
	case "agentMessage", "contextCompaction", "reasoning", "userMessage":
		return true
	default:
		return false
	}
}

func classifyGenerationFailure(raw json.RawMessage) error {
	lower := strings.ToLower(string(raw))
	switch {
	case strings.Contains(lower, "usagelimitexceeded"), strings.Contains(lower, "usage_limit"):
		return errors.New("subscription usage limit reached")
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication"):
		return errors.New("authentication rejected; sign in again")
	default:
		return errors.New("generation failed")
	}
}
