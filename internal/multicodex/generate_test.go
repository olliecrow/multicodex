package multicodex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		{name: "search", args: []string{"--search", "hello"}, want: generateCommandOptions{webSearch: true, prompt: "hello"}},
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
	prompt, err := readBoundedPrompt(strings.NewReader(strings.Repeat("x", generateInputLimit)))
	if err != nil || len(prompt) != generateInputLimit {
		t.Fatalf("limit prompt: len=%d err=%v", len(prompt), err)
	}
	if _, err := readBoundedPrompt(strings.NewReader(strings.Repeat("x", generateInputLimit+1))); err == nil {
		t.Fatal("oversized prompt succeeded")
	}
	if _, err := readBoundedPrompt(errorReader{}); err == nil {
		t.Fatal("input error was ignored")
	}
}

func TestParseGenerateCustomFilesAndJSON(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.txt")
	developerPath := filepath.Join(dir, "developer.txt")
	schemaPath := filepath.Join(dir, "schema.json")
	for path, content := range map[string]string{
		basePath:      "synthetic base instructions\n",
		developerPath: "synthetic developer instructions\n",
		schemaPath:    `{"type":"object","properties":{"answer":{"type":"string"}}}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	options, err := parseGenerateArgs([]string{
		"--effort", "high",
		"--base-instructions-file", basePath,
		"--developer-instructions-file", developerPath,
		"--output-schema", schemaPath,
		"--search",
		"--json",
		"synthetic prompt",
	}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if options.effort != "high" || options.baseInstructions != "synthetic base instructions\n" ||
		options.developerInstructions != "synthetic developer instructions\n" ||
		options.outputSchema != `{"type":"object","properties":{"answer":{"type":"string"}}}` ||
		!options.jsonOutput || !options.webSearch || options.prompt != "synthetic prompt" {
		t.Fatalf("unexpected options: %#v", options)
	}
}

func TestParseGenerateRejectsUnsafeInputFiles(t *testing.T) {
	dir := t.TempDir()
	arraySchema := filepath.Join(dir, "array-schema.json")
	invalidSchema := filepath.Join(dir, "invalid-schema.json")
	largeInstructions := filepath.Join(dir, "large-instructions.txt")
	if err := os.WriteFile(arraySchema, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidSchema, []byte(`{"type":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(largeInstructions, []byte(strings.Repeat("x", generateInputLimit+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		args      []string
		wantError string
	}{
		{name: "array schema", args: []string{"--output-schema", arraySchema, "prompt"}, wantError: "must be a JSON object"},
		{name: "invalid schema", args: []string{"--output-schema", invalidSchema, "prompt"}, wantError: "must be a JSON object"},
		{name: "large instructions", args: []string{"--base-instructions-file", largeInstructions, "prompt"}, wantError: "exceeds 4 MiB"},
		{name: "directory", args: []string{"--developer-instructions-file", dir, "prompt"}, wantError: "must be a regular file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseGenerateArgs(test.args, strings.NewReader(""))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}

	missing := filepath.Join(dir, "private-missing-name.txt")
	_, err := parseGenerateArgs([]string{"--base-instructions-file", missing, "prompt"}, strings.NewReader(""))
	if err == nil || strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), "private-missing-name") {
		t.Fatalf("missing-file error exposed path or did not fail: %v", err)
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

func TestCmdGeneratePassesCustomOptions(t *testing.T) {
	app, _ := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.txt")
	developerPath := filepath.Join(dir, "developer.txt")
	schemaPath := filepath.Join(dir, "schema.json")
	for path, content := range map[string]string{
		basePath: "base", developerPath: "developer", schemaPath: `{"type":"object"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	originalGenerate := generateWithSubscription
	generateWithSubscription = func(_ context.Context, options codexappserver.GenerateOptions) error {
		if options.ActiveProfile != "alpha" || options.Model != "gpt-test" || options.Effort != "high" ||
			options.BaseInstructions != "base" || options.DeveloperInstructions != "developer" ||
			string(options.OutputSchema) != `{"type":"object"}` || !options.JSONOutput || !options.WebSearch || options.Prompt != "hello" {
			return fmt.Errorf("unexpected generation options")
		}
		return nil
	}
	defer func() { generateWithSubscription = originalGenerate }()

	err := app.Run([]string{
		"generate", "--account", "alpha", "--model", "gpt-test", "--effort", "high",
		"--base-instructions-file", basePath, "--developer-instructions-file", developerPath,
		"--output-schema", schemaPath, "--search", "--json", "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSelectGenerateAccountHonorsCancellation(t *testing.T) {
	app, _ := newExecTestApp(t)
	createExecProfiles(t, app, "alpha")

	originalSelector := defaultExecAccountSelector
	defaultExecAccountSelector = func(ctx context.Context, _ []usage.MonitorAccount, _ string) (usage.SelectedAccount, error) {
		if err := ctx.Err(); err != nil {
			return usage.SelectedAccount{}, err
		}
		return usage.SelectedAccount{}, errors.New("selector context was not canceled")
	}
	defer func() { defaultExecAccountSelector = originalSelector }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.selectGenerateAccount(ctx, generateCommandOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("selection error = %v, want context canceled", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic read failure")
}
