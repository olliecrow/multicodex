package multicodex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/olliecrow/multicodex/internal/codexappserver"
	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestParseGenerateArgs(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		stdin     string
		want      generateCommandOptions
		wantCode  int
		wantError string
	}{
		{name: "argument", args: []string{"--account", "alpha", "-m", "gpt-test", "hello"}, want: generateCommandOptions{account: "alpha", accountSet: true, model: "gpt-test", prompt: "hello"}},
		{name: "stdin", args: []string{"--model=gpt-test"}, stdin: "hello from stdin\n", want: generateCommandOptions{model: "gpt-test", prompt: "hello from stdin\n"}},
		{name: "dash prompt", args: []string{"--", "-hello"}, want: generateCommandOptions{prompt: "-hello"}},
		{name: "empty", stdin: " \n", wantCode: 2, wantError: "prompt is empty"},
		{name: "empty account", args: []string{"--account", "", "hello"}, wantCode: 2, wantError: generateUsage},
		{name: "too many prompts", args: []string{"one", "two"}, wantCode: 2, wantError: generateUsage},
		{name: "unknown flag", args: []string{"--unknown", "hello"}, wantCode: 2, wantError: generateUsage},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGenerateArgs(test.args, strings.NewReader(test.stdin))
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got != test.want {
					t.Fatalf("options = %#v, want %#v", got, test.want)
				}
				return
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != test.wantCode || !strings.Contains(exitErr.Message, test.wantError) {
				t.Fatalf("error = %#v, want code %d containing %q", err, test.wantCode, test.wantError)
			}
		})
	}
}

func TestReadBoundedPrompt(t *testing.T) {
	prompt, err := readBoundedPrompt(strings.NewReader(strings.Repeat("x", generatePromptLimit)))
	if err != nil || len(prompt) != generatePromptLimit {
		t.Fatalf("limit prompt: len=%d err=%v", len(prompt), err)
	}
	if _, err := readBoundedPrompt(strings.NewReader(strings.Repeat("x", generatePromptLimit+1))); err == nil {
		t.Fatal("oversized prompt succeeded")
	}
	if _, err := readBoundedPrompt(errorReader{}); err == nil {
		t.Fatal("input error was ignored")
	}
}

func TestCmdGenerateRoutesAndWritesOnlyGeneratedText(t *testing.T) {
	app, _ := newExecTestApp(t)
	createExecProfiles(t, app, "alpha", "beta")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(_ context.Context, _ []usage.MonitorAccount, model string) (usage.SelectedAccount, error) {
		if model != "gpt-test" {
			t.Fatalf("selector model = %q", model)
		}
		return usage.SelectedAccount{Account: usage.MonitorAccount{Label: "beta"}, WeeklyUsedPercent: 15}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	originalGenerate := generateWithSubscription
	generateWithSubscription = func(_ context.Context, options codexappserver.GenerateOptions) error {
		if options.ActiveProfile != "beta" || options.Model != "gpt-test" || options.Prompt != "hello" {
			return fmt.Errorf("unexpected options: profile=%q model=%q prompt=%q", options.ActiveProfile, options.Model, options.Prompt)
		}
		_, err := io.WriteString(options.Output, "generated text")
		return err
	}
	defer func() { generateWithSubscription = originalGenerate }()

	output, err := captureStdout(t, func() error {
		return app.Run([]string{"generate", "--model", "gpt-test", "hello"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if output != "generated text" {
		t.Fatalf("stdout = %q", output)
	}
}

func TestCmdGenerateUsesExplicitProfile(t *testing.T) {
	app, _ := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(context.Context, []usage.MonitorAccount, string) (usage.SelectedAccount, error) {
		t.Fatal("usage selector was called for an explicit account")
		return usage.SelectedAccount{}, nil
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	originalGenerate := generateWithSubscription
	generateWithSubscription = func(_ context.Context, options codexappserver.GenerateOptions) error {
		if options.ActiveProfile != "alpha" || options.Prompt != "hello" {
			return fmt.Errorf("unexpected options: profile=%q prompt=%q", options.ActiveProfile, options.Prompt)
		}
		return nil
	}
	defer func() { generateWithSubscription = originalGenerate }()

	if err := app.Run([]string{"generate", "--account", "alpha", "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"generate", "--account", "missing", "hello"}); err == nil {
		t.Fatal("unknown explicit account succeeded")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic read failure")
}
