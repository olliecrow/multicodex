package multicodex

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func RunCodexLogin(codexHome string, extraArgs []string) error {
	args := append([]string{"login"}, extraArgs...)
	cmd := exec.Command("codex", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = withProfileEnv(os.Environ(), codexHome, "")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Message: "codex login failed"}
		}
		return fmt.Errorf("run codex login: %w", err)
	}
	return nil
}

func RunCommand(bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Message: fmt.Sprintf("command failed: %s", strings.Join(append([]string{bin}, args...), " "))}
		}
		return fmt.Errorf("run command: %w", err)
	}
	return nil
}

func RunShellWithProfile(codexHome, profile string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = withProfileEnv(os.Environ(), codexHome, profile)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Message: "profile shell exited with error"}
		}
		return fmt.Errorf("run profile shell: %w", err)
	}
	return nil
}

func RunWithProfile(codexHome, profile, bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = withProfileEnv(os.Environ(), codexHome, profile)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Message: fmt.Sprintf("command failed: %s", strings.Join(append([]string{bin}, args...), " "))}
		}
		return fmt.Errorf("run command with profile: %w", err)
	}
	return nil
}

func withProfileEnv(base []string, codexHome, profile string) []string {
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "CODEX_HOME=") || strings.HasPrefix(kv, "MULTICODEX_ACTIVE_PROFILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CODEX_HOME="+codexHome)
	if profile != "" {
		env = append(env, "MULTICODEX_ACTIVE_PROFILE="+profile)
	}
	return env
}

func RenderShellExports(codexHome, profile string) string {
	var b strings.Builder
	b.WriteString("export CODEX_HOME=")
	b.WriteString(strconv.Quote(codexHome))
	b.WriteString("\n")
	b.WriteString("export MULTICODEX_ACTIVE_PROFILE=")
	b.WriteString(strconv.Quote(profile))
	b.WriteString("\n")
	return b.String()
}
