package multicodex

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

var execLookPath = exec.LookPath
var syscallExec = syscall.Exec
var isInteractiveTerminalAttached = func() bool {
	return fileIsTerminal(os.Stdin) && fileIsTerminal(os.Stdout) && fileIsTerminal(os.Stderr)
}

func RunCodexLogin(codexHome string, extraArgs []string) error {
	return runCommandWithEnv("codex", append([]string{"login"}, extraArgs...), profileCodexEnv(os.Environ(), codexHome, ""), "codex login failed")
}

func RunCodexWithProfile(codexHome, profile string, args []string) error {
	return runCommandWithEnv("codex", args, profileCodexEnv(os.Environ(), codexHome, profile), "codex command failed")
}

func RunInteractiveCodexWithProfile(codexHome, profile string, args []string) error {
	env := profileCodexEnv(os.Environ(), codexHome, profile)
	if isInteractiveTerminalAttached() {
		path, err := execLookPath("codex")
		if err != nil {
			return fmt.Errorf("find command codex: %w", err)
		}
		return syscallExec(path, append([]string{"codex"}, args...), env)
	}
	return runCommandWithEnv("codex", args, env, fmt.Sprintf("codex command failed: %s", strings.Join(append([]string{"codex"}, args...), " ")))
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

func profileCodexEnv(base []string, codexHome, profile string) []string {
	env := sanitizedCodexEnv(base, codexHome)
	if profile != "" {
		env = append(env, "MULTICODEX_ACTIVE_PROFILE="+profile)
	}
	return env
}

func neutralCodexEnv(base []string) []string {
	return sanitizedCodexEnv(base, "")
}

func sanitizedCodexEnv(base []string, codexHome string) []string {
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if codexEnvVarShouldBeStripped(key) {
			continue
		}
		env = append(env, kv)
	}
	if codexHome != "" {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	return env
}

func codexEnvVarShouldBeStripped(key string) bool {
	switch key {
	case "CODEX_HOME",
		"MULTICODEX_ACTIVE_PROFILE",
		"MULTICODEX_SELECTED_PROFILE_PATH",
		"MULTICODEX_HEARTBEAT_LOCK_PATH",
		"MULTICODEX_HEARTBEAT_PROMPT",
		"OPENAI_API_KEY",
		"OPENAI_ORG_ID",
		"OPENAI_ORGANIZATION",
		"OPENAI_PROJECT",
		"OPENAI_BASE_URL",
		"OPENAI_API_BASE",
		"OPENAI_HOST",
		"CODEX_API_KEY",
		"CODEX_AUTH_TOKEN",
		"CODEX_ACCESS_TOKEN",
		"CODEX_REFRESH_TOKEN",
		"CODEX_TOKEN",
		"CODEX_BASE_URL",
		"CODEX_API_BASE":
		return true
	default:
		return false
	}
}

func shellQuoteValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
