package multicodex

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// App wires command handlers with persistent config.
type App struct {
	store *Store
}

func NewApp() (*App, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}
	return &App{store: NewStore(paths)}, nil
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		return a.cmdHelp(args[1:])
	case "version", "-v", "--version":
		printVersion()
		return nil
	case "init":
		return a.cmdInit()
	case "add":
		return a.cmdAdd(args[1:])
	case "login":
		return a.cmdLogin(args[1:])
	case "login-all":
		return a.cmdLoginAll()
	case "use":
		return a.cmdUse(args[1:])
	case "app":
		return a.cmdApp(args[1:])
	case "run":
		return a.cmdRun(args[1:])
	case "exec":
		return a.cmdExec(args[1:])
	case "switch-global":
		return a.cmdSwitchGlobal(args[1:])
	case "status":
		return a.cmdStatus()
	case "heartbeat":
		return a.cmdHeartbeat(args[1:])
	case "monitor":
		return a.cmdMonitor(args[1:])
	case "completion":
		return a.cmdCompletion(args[1:])
	case "__complete-profiles":
		return a.cmdCompleteProfiles()
	case "doctor":
		return a.cmdDoctor(args[1:])
	case "dry-run":
		return a.cmdDryRun(args[1:])
	default:
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown command: %s\nrun \"multicodex help\" for available commands", args[0])}
	}
}

func printVersion() {
	fmt.Printf("%s %s\n", appName, appVersion)
}

func (a *App) loadOrInitConfig() (*Config, error) {
	cfg, err := a.store.Load()
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		cfg = DefaultConfig()
		if err := a.store.EnsureBaseDirs(); err != nil {
			return nil, err
		}
		if err := a.store.Save(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	return nil, err
}

func (a *App) loadConfigIfExists() (*Config, error) {
	cfg, err := a.store.Load()
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	return nil, err
}

func (a *App) cmdInit() error {
	if err := a.store.EnsureBaseDirs(); err != nil {
		return err
	}

	cfg, err := a.store.Load()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = DefaultConfig()
			if err := a.store.Save(cfg); err != nil {
				return err
			}
			fmt.Println("initialized multicodex local state")
			fmt.Printf("home: %s\n", a.store.paths.MulticodexHome)
			return nil
		}
		return err
	}

	fmt.Println("multicodex already initialized")
	fmt.Printf("home: %s\n", a.store.paths.MulticodexHome)
	fmt.Printf("profiles: %d\n", len(cfg.Profiles))
	return nil
}

func (a *App) cmdAdd(args []string) error {
	if len(args) != 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex add <name>"}
	}
	name := strings.TrimSpace(args[0])
	if err := ValidateProfileName(name); err != nil {
		return &ExitError{Code: 2, Message: err.Error()}
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}

	if _, exists := cfg.Profiles[name]; exists {
		return &ExitError{Code: 2, Message: fmt.Sprintf("profile already exists: %s", name)}
	}

	profile, err := a.store.CreateProfile(name)
	if err != nil {
		return err
	}
	cfg.Profiles[name] = profile
	if err := a.store.Save(cfg); err != nil {
		return err
	}

	fmt.Printf("added profile: %s\n", name)
	fmt.Printf("codex home: %s\n", profile.CodexHome)
	return nil
}

func (a *App) cmdLogin(args []string) error {
	if len(args) < 1 {
		return &ExitError{Code: 2, Message: "usage: multicodex login <name> [codex login args]"}
	}
	name := args[0]
	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}
	if err := ensureLoginConfigReady(a.store.paths, profile); err != nil {
		return err
	}

	fmt.Printf("logging in profile %q\n", name)
	if err := RunCodexLogin(profile.CodexHome, args[1:]); err != nil {
		return err
	}

	hasAuth, err := HasAuthFile(profile.CodexHome)
	if err != nil {
		return err
	}
	if hasAuth {
		if err := secureAuthFilePermissions(profile.CodexHome); err != nil {
			return err
		}
		fmt.Println("login complete")
	} else {
		fmt.Println("login command completed. auth file not detected. this may indicate keychain mode or an incomplete login")
	}
	return nil
}

func ensureLoginConfigReady(paths Paths, profile Profile) error {
	return ensureProfileCodexExecutionReady(paths, profile)
}

func ensureProfileCodexExecutionReady(paths Paths, profile Profile) error {
	configPath := filepath.Join(profile.CodexHome, "config.toml")
	ok, err := profileConfigUsesFileStore(configPath)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return &ExitError{
		Code: 2,
		Message: fmt.Sprintf(
			"profile %q requires file-backed auth to keep auth isolated. set cli_auth_credentials_store = \"file\" in %s or create a per-profile override at %s",
			profile.Name,
			filepath.Join(paths.DefaultCodexHome, "config.toml"),
			configPath,
		),
	}
}

func commandRequiresProfileCodexIsolation(cmd string) bool {
	base := filepath.Base(strings.TrimSpace(cmd))
	return base == "codex"
}

func (a *App) cmdLoginAll() error {
	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		return &ExitError{Code: 2, Message: "no profiles configured. add one with: multicodex add <name>"}
	}

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	failed := 0
	for _, name := range names {
		fmt.Printf("\n== %s ==\n", name)
		if err := a.cmdLogin([]string{name}); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "login failed for %s: %v\n", name, err)
		}
	}
	if failed > 0 {
		return &ExitError{Code: 1, Message: fmt.Sprintf("login-all completed with %d failure(s)", failed)}
	}
	fmt.Println("login-all completed")
	return nil
}

func (a *App) cmdUse(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return &ExitError{Code: 2, Message: "usage: multicodex use <name> [--shell]"}
	}
	name := args[0]
	openShell := len(args) == 2 && args[1] == "--shell"
	if len(args) == 2 && args[1] != "--shell" {
		return &ExitError{Code: 2, Message: "usage: multicodex use <name> [--shell]"}
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}

	if openShell {
		fmt.Printf("starting shell with profile %q\n", name)
		return RunShellWithProfile(profile.CodexHome, name)
	}

	fmt.Print(RenderShellExports(profile.CodexHome, name))
	return nil
}

func (a *App) cmdRun(args []string) error {
	if len(args) < 3 {
		return &ExitError{Code: 2, Message: "usage: multicodex run <name> -- <command...>"}
	}
	name := args[0]
	sep := -1
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep == len(args)-1 {
		return &ExitError{Code: 2, Message: "usage: multicodex run <name> -- <command...>"}
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}
	cmd := args[sep+1]
	cmdArgs := args[sep+2:]
	if commandRequiresProfileCodexIsolation(cmd) {
		if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
			return err
		}
	}
	return RunWithProfile(profile.CodexHome, name, cmd, cmdArgs)
}

func (a *App) cmdSwitchGlobal(args []string) error {
	name, restoreDefault, force, err := parseSwitchGlobalArgs(args)
	if err != nil {
		return err
	}

	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}

	if restoreDefault {
		changed, err := a.store.RestoreGlobalAuth(cfg)
		if err != nil {
			return err
		}
		if err := a.store.Save(cfg); err != nil {
			return err
		}
		if changed {
			fmt.Println("restored global default auth")
		} else {
			fmt.Println("no global backup state found. nothing to restore")
		}
		return nil
	}

	profile, ok := cfg.Profiles[name]
	if !ok {
		return &ExitError{Code: 2, Message: fmt.Sprintf("unknown profile: %s", name)}
	}
	if err := a.store.EnsureProfileDir(profile); err != nil {
		return err
	}
	if err := ensureProfileCodexExecutionReady(a.store.paths, profile); err != nil {
		if !force {
			return err
		}
		fmt.Fprintf(os.Stderr, "warning: forcing global switch despite disabled file-backed auth isolation: %v\n", err)
	}
	if err := a.store.SwitchGlobalAuthToProfile(cfg, profile); err != nil {
		return err
	}
	if err := a.store.Save(cfg); err != nil {
		return err
	}

	fmt.Printf("global codex auth now points to profile %q\n", name)
	fmt.Println("note: only auth pointer was switched. unrelated codex files were left untouched")
	if force {
		fmt.Println("warning: forced switch bypassed file-backed auth isolation preflight")
	}
	return nil
}

func parseSwitchGlobalArgs(args []string) (string, bool, bool, error) {
	usageErr := &ExitError{Code: 2, Message: "usage: multicodex switch-global <name> [--force] | --restore-default"}
	if len(args) == 0 || len(args) > 2 {
		return "", false, false, usageErr
	}

	var (
		name           string
		restoreDefault bool
		force          bool
	)
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "":
			return "", false, false, usageErr
		case "--restore-default":
			restoreDefault = true
		case "--force":
			force = true
		default:
			if name != "" {
				return "", false, false, usageErr
			}
			name = arg
		}
	}

	if restoreDefault {
		if force || name != "" {
			return "", false, false, usageErr
		}
		return "", true, false, nil
	}
	if name == "" {
		return "", false, false, usageErr
	}
	return name, false, force, nil
}

func (a *App) cmdStatus() error {
	cfg, err := a.loadOrInitConfig()
	if err != nil {
		return err
	}
	return PrintStatus(a.store, cfg)
}

func (a *App) cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "output doctor report as JSON")
	timeout := fs.Duration("timeout", 8*time.Second, "timeout for command checks")
	if err := fs.Parse(args); err != nil {
		return &ExitError{Code: 2, Message: "usage: multicodex doctor [--json] [--timeout 8s]"}
	}
	if *timeout <= 0 {
		return &ExitError{Code: 2, Message: "error: --timeout must be > 0"}
	}

	cfg, err := a.loadConfigIfExists()
	if err != nil {
		return err
	}
	report := RunDoctor(a.store, cfg, *timeout)
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printDoctorHuman(report)
	}
	if report.HasFailures() {
		return &ExitError{Code: 1, Message: "doctor checks failed"}
	}
	return nil
}

func (a *App) cmdDryRun(args []string) error {
	cfg, err := a.loadConfigIfExists()
	if err != nil {
		return err
	}
	text, err := RenderDryRun(a.store, cfg, args)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}
