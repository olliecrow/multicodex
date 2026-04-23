package multicodex

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

var execLookPath = exec.LookPath
var syscallExec = syscall.Exec
var isInteractiveTerminalAttached = func() bool {
	return fileIsTerminal(os.Stdin) && fileIsTerminal(os.Stdout) && fileIsTerminal(os.Stderr)
}

func RunCodexLogin(codexHome string, extraArgs []string) error {
	return runCommandWithEnv("codex", append([]string{"login"}, extraArgs...), withProfileEnv(os.Environ(), codexHome, ""), "codex login failed")
}

func RunCommand(bin string, args []string) error {
	return runCommandWithEnv(bin, args, nil, fmt.Sprintf("command failed: %s", strings.Join(append([]string{bin}, args...), " ")))
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
	return runCommandWithEnv(bin, args, withProfileEnv(os.Environ(), codexHome, profile), fmt.Sprintf("command failed: %s", strings.Join(append([]string{bin}, args...), " ")))
}

func RunInteractiveWithProfile(codexHome, profile, bin string, args []string) error {
	env := withProfileEnv(os.Environ(), codexHome, profile)
	if isInteractiveTerminalAttached() {
		path, err := execLookPath(bin)
		if err != nil {
			return fmt.Errorf("find command %s: %w", bin, err)
		}
		return syscallExec(path, append([]string{bin}, args...), env)
	}
	return runCommandWithEnv(bin, args, env, fmt.Sprintf("command failed: %s", strings.Join(append([]string{bin}, args...), " ")))
}

func runCommandWithEnv(bin string, args []string, env []string, exitMessage string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = env
	}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Message: exitMessage}
		}
		return fmt.Errorf("run command %s: %w", bin, err)
	}
	return nil
}

func fileIsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
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
