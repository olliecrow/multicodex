package multicodex

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/olliecrow/multicodex/internal/codexappserver"
)

const (
	generateInputLimit = 4 * 1024 * 1024
	generateUsage      = "usage: multicodex generate [--account <name>] [-m|--model <model>] [--effort <effort>] [--base-instructions-file <path>] [--developer-instructions-file <path>] [--output-schema <path>] [--json] [prompt]"
)

var generateWithSubscription = codexappserver.Generate

type generateCommandOptions struct {
	account               string
	accountSet            bool
	model                 string
	effort                string
	baseInstructions      string
	developerInstructions string
	outputSchema          string
	jsonOutput            bool
	prompt                string
}

func (a *App) cmdGenerate(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return a.cmdHelp([]string{"generate"})
	}
	options, err := parseGenerateArgs(args, os.Stdin)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	selected, err := a.selectGenerateAccount(ctx, options)
	if err != nil {
		return err
	}
	if err := writeSelectedProfileMetadata(a.store.paths, os.Getenv(envSelectedProfilePath), selected.Metadata); err != nil {
		return err
	}

	activeProfile := selected.Name
	if !selected.IsProfile {
		activeProfile = ""
	}
	return generateWithSubscription(ctx, codexappserver.GenerateOptions{
		CodexHome:             selected.CodexHome,
		ActiveProfile:         activeProfile,
		Model:                 options.model,
		Effort:                options.effort,
		BaseInstructions:      options.baseInstructions,
		DeveloperInstructions: options.developerInstructions,
		OutputSchema:          json.RawMessage(options.outputSchema),
		JSONOutput:            options.jsonOutput,
		Prompt:                options.prompt,
		Output:                os.Stdout,
	})
}

func (a *App) selectGenerateAccount(ctx context.Context, options generateCommandOptions) (execSelection, error) {
	if !options.accountSet {
		selectorArgs := []string(nil)
		if options.model != "" {
			selectorArgs = []string{"--model", options.model}
		}
		selected, err := a.selectAccountForCodexArgs(ctx, selectorArgs)
		if err != nil {
			return execSelection{}, err
		}
		if selected.IsProfile {
			if err := ensureProfileCodexExecutionReady(a.store.paths, selected.Profile); err != nil {
				return execSelection{}, err
			}
		}
		return selected, nil
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return execSelection{}, err
	}
	profile, ok := cfg.Profiles[options.account]
	if !ok {
		return execSelection{}, &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", options.account)}
	}
	changes, err := a.store.EnsureProfileDir(profile, cfg.ProfileResources)
	if err != nil {
		return execSelection{}, err
	}
	printResourceChangesToStderr(changes)
	if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
		return execSelection{}, err
	}
	return execSelection{
		Name:      options.account,
		CodexHome: profile.CodexHome,
		IsProfile: true,
		Profile:   profile,
		Metadata: execSelectionMetadata{
			Profile:         options.account,
			SelectionSource: "explicit_account",
		},
	}, nil
}

func parseGenerateArgs(args []string, input io.Reader) (generateCommandOptions, error) {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options generateCommandOptions
	var baseInstructionsPath string
	var developerInstructionsPath string
	var outputSchemaPath string
	flags.StringVar(&options.account, "account", "", "profile name")
	flags.StringVar(&options.model, "model", "", "model")
	flags.StringVar(&options.model, "m", "", "model")
	flags.StringVar(&options.effort, "effort", "", "reasoning effort")
	flags.StringVar(&baseInstructionsPath, "base-instructions-file", "", "base instructions file")
	flags.StringVar(&developerInstructionsPath, "developer-instructions-file", "", "developer instructions file")
	flags.StringVar(&outputSchemaPath, "output-schema", "", "JSON output schema file")
	flags.BoolVar(&options.jsonOutput, "json", false, "emit text and sanitized generation metrics as JSON")
	if err := flags.Parse(args); err != nil {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: generateUsage}
	}
	remaining := flags.Args()
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "account" {
			options.accountSet = true
		}
	})
	if len(remaining) > 1 {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: generateUsage}
	}
	if len(remaining) == 1 {
		options.prompt = remaining[0]
	} else {
		prompt, err := readBoundedPrompt(input)
		if err != nil {
			return generateCommandOptions{}, err
		}
		options.prompt = prompt
	}
	options.account = strings.TrimSpace(options.account)
	options.model = strings.TrimSpace(options.model)
	options.effort = strings.TrimSpace(options.effort)
	if options.accountSet && options.account == "" {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: generateUsage}
	}
	if strings.TrimSpace(options.prompt) == "" {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: "generation prompt is empty\n" + generateUsage}
	}
	if baseInstructionsPath != "" {
		data, err := readBoundedGenerationFile(baseInstructionsPath, "base instructions")
		if err != nil {
			return generateCommandOptions{}, err
		}
		options.baseInstructions = string(data)
	}
	if developerInstructionsPath != "" {
		data, err := readBoundedGenerationFile(developerInstructionsPath, "developer instructions")
		if err != nil {
			return generateCommandOptions{}, err
		}
		options.developerInstructions = string(data)
	}
	if outputSchemaPath != "" {
		data, err := readBoundedGenerationFile(outputSchemaPath, "output schema")
		if err != nil {
			return generateCommandOptions{}, err
		}
		var schema map[string]any
		if json.Unmarshal(data, &schema) != nil || schema == nil {
			return generateCommandOptions{}, &ExitError{Code: 2, Message: "generation output schema must be a JSON object"}
		}
		options.outputSchema = string(data)
	}
	return options, nil
}

func readBoundedPrompt(input io.Reader) (string, error) {
	if input == nil {
		return "", errors.New("read generation prompt: input is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(input, generateInputLimit+1))
	if err != nil {
		return "", fmt.Errorf("read generation prompt: %w", err)
	}
	if len(data) > generateInputLimit {
		return "", &ExitError{Code: 2, Message: "generation prompt exceeds 4 MiB"}
	}
	return string(data), nil
}

func readBoundedGenerationFile(path, label string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read generation %s file: unavailable", label)
	}
	if !info.Mode().IsRegular() {
		return nil, &ExitError{Code: 2, Message: fmt.Sprintf("generation %s file must be a regular file", label)}
	}
	if info.Size() > generateInputLimit {
		return nil, &ExitError{Code: 2, Message: fmt.Sprintf("generation %s file exceeds 4 MiB", label)}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read generation %s file: unavailable", label)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, generateInputLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read generation %s file: failed", label)
	}
	if len(data) > generateInputLimit {
		return nil, &ExitError{Code: 2, Message: fmt.Sprintf("generation %s file exceeds 4 MiB", label)}
	}
	return data, nil
}
