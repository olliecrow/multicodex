package multicodex

import (
	"context"
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
	generatePromptLimit = 4 * 1024 * 1024
	generateUsage       = "usage: multicodex generate [--account <name>] [-m|--model <model>] [prompt]"
)

var generateWithSubscription = codexappserver.Generate

type generateCommandOptions struct {
	account    string
	accountSet bool
	model      string
	prompt     string
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
		CodexHome:     selected.CodexHome,
		ActiveProfile: activeProfile,
		Model:         options.model,
		Prompt:        options.prompt,
		Output:        os.Stdout,
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
	flags.StringVar(&options.account, "account", "", "profile name")
	flags.StringVar(&options.model, "model", "", "model")
	flags.StringVar(&options.model, "m", "", "model")
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
	if options.accountSet && options.account == "" {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: generateUsage}
	}
	if strings.TrimSpace(options.prompt) == "" {
		return generateCommandOptions{}, &ExitError{Code: 2, Message: "generation prompt is empty\n" + generateUsage}
	}
	return options, nil
}

func readBoundedPrompt(input io.Reader) (string, error) {
	if input == nil {
		return "", errors.New("read generation prompt: input is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(input, generatePromptLimit+1))
	if err != nil {
		return "", fmt.Errorf("read generation prompt: %w", err)
	}
	if len(data) > generatePromptLimit {
		return "", &ExitError{Code: 2, Message: "generation prompt exceeds 4 MiB"}
	}
	return string(data), nil
}
