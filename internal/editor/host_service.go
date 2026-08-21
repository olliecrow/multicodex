package editor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type commandRunner interface {
	run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

type commandFailure struct {
	exitCode int
	notFound bool
}

func (e commandFailure) Error() string { return "external command failed" }

func (execRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	killGroup := filepath.Base(name) != "tmux"
	if killGroup {
		// A new session removes the controlling terminal, so Git credential and
		// SSH helpers cannot open a hidden prompt behind the raw-screen UI.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		cmd.WaitDelay = 2 * time.Second
	}
	cmd.Env = sanitizedCommandEnvironment(os.Environ(), name)
	var stdout bytes.Buffer
	cmd.Stdout = &limitedWriter{dst: &stdout, remaining: 4 << 20}
	cmd.Stderr = &limitedWriter{remaining: 64 << 10}
	if err := cmd.Run(); err != nil {
		failure := commandFailure{exitCode: -1}
		var exitError *exec.ExitError
		var executableError *exec.Error
		if errors.As(err, &exitError) {
			failure.exitCode = exitError.ExitCode()
		}
		if errors.As(err, &executableError) {
			failure.notFound = true
		}
		return nil, failure
	}
	return stdout.Bytes(), nil
}

func sanitizedCommandEnvironment(environment []string, command string) []string {
	command = filepath.Base(command)
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if command == "tmux" && (key == "TMUX" || key == "TMUX_TMPDIR") {
			continue
		}
		if command == "git" && unsafeGitEnvironmentKey(key) {
			continue
		}
		result = append(result, item)
	}
	if command == "git" {
		result = replaceEnvironment(result, "GIT_TERMINAL_PROMPT", "0")
		result = replaceEnvironment(result, "GCM_INTERACTIVE", "Never")
		result = replaceEnvironment(result, "GIT_ASKPASS", "")
		result = replaceEnvironment(result, "SSH_ASKPASS", "")
		result = replaceEnvironment(result, "SSH_ASKPASS_REQUIRE", "never")
	}
	return result
}

func unsafeGitEnvironmentKey(key string) bool {
	switch key {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_ASKPASS",
		"SSH_ASKPASS", "SSH_ASKPASS_REQUIRE":
		return true
	default:
		return strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_")
	}
}

type limitedWriter struct {
	dst       *bytes.Buffer
	remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	if len(p) > w.remaining {
		p = p[:max(0, w.remaining)]
	}
	if w.dst != nil && len(p) > 0 {
		_, _ = w.dst.Write(p)
	}
	w.remaining -= len(p)
	return original, nil
}

type HostService struct {
	store  *hostStore
	runner commandRunner
	now    func() time.Time
}

func NewHostService(multicodexHome, instanceID string) (*HostService, error) {
	store, err := newHostStore(multicodexHome, instanceID)
	if err != nil {
		return nil, err
	}
	return &HostService{store: store, runner: execRunner{}, now: time.Now}, nil
}

func (s *HostService) socketName() string {
	return "mce-" + s.store.instanceID[:12]
}

func (s *HostService) tmuxSocketPath() string {
	return filepath.Join("/tmp", "tmux-"+strconv.Itoa(os.Getuid()), s.socketName())
}

func (s *HostService) sessionName(windowID string) string {
	return "mce-" + windowID
}

func (s *HostService) Snapshot(ctx context.Context) (HostSnapshot, error) {
	var registry hostRegistry
	if err := s.store.withReadLock(func(current hostRegistry) error {
		registry = current
		registry.Workspaces = append([]Workspace(nil), current.Workspaces...)
		registry.Windows = append([]Window(nil), current.Windows...)
		return nil
	}); err != nil {
		return HostSnapshot{}, err
	}
	snapshot := HostSnapshot{Protocol: hostProtocol}
	for _, workspace := range registry.Workspaces {
		if !workspace.CreatePending {
			snapshot.Workspaces = append(snapshot.Workspaces, workspace)
		}
	}
	windows := make([]Window, 0, len(registry.Windows))
	for _, window := range registry.Windows {
		if window.CreatePending {
			continue
		}
		state, alive, err := s.inspectSession(ctx, window)
		if err != nil {
			return HostSnapshot{}, err
		}
		if state != sessionOwned {
			window.Alive = false
			windows = append(windows, window)
			continue
		}
		window.Alive = alive
		capture, err := s.tmux(ctx, "capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(activityRows), "-t", window.Session)
		if err != nil {
			currentState, currentAlive, inspectErr := s.inspectSession(ctx, window)
			if inspectErr != nil || currentState == sessionOwned && currentAlive {
				return HostSnapshot{}, fmt.Errorf("capture owned terminal %q: %w", window.Name, err)
			}
			window.Alive = false
			windows = append(windows, window)
			continue
		}
		hash := sha256.Sum256(capture)
		window.PaneHash = hex.EncodeToString(hash[:])
		windows = append(windows, window)
	}
	snapshot.Windows = windows
	return snapshot, nil
}

func (s *HostService) CreateWorkspace(ctx context.Context, request CreateWorkspaceRequest) (Workspace, error) {
	if err := validateID(request.ProjectID, "project identifier"); err != nil {
		return Workspace{}, err
	}
	if err := validateAbsolutePath(request.ProjectPath, "project path"); err != nil {
		return Workspace{}, err
	}
	if err := validateName(request.Name, "workspace name"); err != nil {
		return Workspace{}, err
	}
	if request.BaseRemote != "" && !safeGitRefPart(request.BaseRemote) {
		return Workspace{}, errors.New("base remote contains unsupported characters")
	}
	if request.BaseBranch != "" && !safeGitRefPart(request.BaseBranch) {
		return Workspace{}, errors.New("base branch contains unsupported characters")
	}
	info, err := os.Stat(request.ProjectPath)
	if err != nil || !info.IsDir() {
		return Workspace{}, errors.New("project path is not an accessible directory")
	}

	id, err := newID()
	if err != nil {
		return Workspace{}, err
	}
	now := s.now().UTC()
	workspace := Workspace{
		ID: id, ProjectID: request.ProjectID, ProjectPath: request.ProjectPath,
		Name: request.Name, CreatedAt: now, LastUsedAt: now,
	}
	gitRoot, isGit, err := s.gitRoot(ctx, request.ProjectPath)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Git = isGit
	if isGit {
		if !sameDirectory(gitRoot, request.ProjectPath) {
			return Workspace{}, errors.New("Git project path must be the repository root")
		}
		workspace.GitCommonDir, err = s.gitCommonDir(ctx, request.ProjectPath)
		if err != nil {
			return Workspace{}, err
		}
		branch, baseRef, worktreePath, err := s.prepareGitWorktree(ctx, request, id)
		if err != nil {
			return Workspace{}, err
		}
		workspace.Branch, workspace.BaseRef, workspace.Path = branch, baseRef, worktreePath
		workspace.CreatePending = true
	} else {
		workspace.Path = request.ProjectPath
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for _, existing := range registry.Workspaces {
			if existing.ProjectID == request.ProjectID && existing.Name == request.Name {
				return errors.New("a workspace with this name already exists in the project")
			}
		}
		if !workspace.Git {
			for _, existing := range registry.Workspaces {
				if existing.ProjectID == request.ProjectID && !existing.Git {
					return errors.New("a non-Git project can have only one in-place workspace")
				}
			}
		}
		registry.Workspaces = append(registry.Workspaces, workspace)
		return nil
	}); err != nil {
		return Workspace{}, err
	}
	if workspace.Git {
		if err := s.addGitWorktree(ctx, workspace); err != nil {
			s.rollbackWorkspaceCreation(workspace, false)
			return Workspace{}, err
		}
		if err := s.store.withLock(func(registry *hostRegistry) error {
			for i := range registry.Workspaces {
				if registry.Workspaces[i].ID == workspace.ID {
					registry.Workspaces[i].CreatePending = false
					return nil
				}
			}
			return errors.New("workspace creation record disappeared")
		}); err != nil {
			s.rollbackWorkspaceCreation(workspace, true)
			return Workspace{}, err
		}
		workspace.CreatePending = false
	}
	return workspace, nil
}

func (s *HostService) rollbackWorkspaceCreation(workspace Workspace, branchOwned bool) {
	clean := true
	if workspace.Git {
		rollbackContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if branchOwned {
			clean = s.removeGitWorktree(rollbackContext, workspace, true) == nil
		} else if _, err := os.Lstat(workspace.Path); err == nil {
			// An exact worktree on the recorded branch proves that the failed Git
			// invocation created the resource. Otherwise preserve all state.
			clean = s.verifyGitWorktree(rollbackContext, workspace) == nil && s.removeGitWorktree(rollbackContext, workspace, true) == nil
		} else if errors.Is(err, os.ErrNotExist) {
			exists, branchErr := s.gitBranchExists(rollbackContext, workspace.ProjectPath, workspace.Branch)
			clean = branchErr == nil && !exists
		} else {
			clean = false
		}
	}
	if !clean {
		return
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		kept := registry.Workspaces[:0]
		for _, candidate := range registry.Workspaces {
			if candidate.ID != workspace.ID {
				kept = append(kept, candidate)
			}
		}
		registry.Workspaces = kept
		return nil
	}); err == nil {
		s.removeEmptyWorktreeDirs(workspace.ProjectID)
	}
}

func (s *HostService) InspectProject(ctx context.Context, path string) (ProjectInfo, error) {
	if err := validateAbsolutePath(path, "project path"); err != nil {
		return ProjectInfo{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ProjectInfo{}, errors.New("project path is not an accessible directory")
	}
	root, isGit, err := s.gitRoot(ctx, path)
	if err != nil {
		return ProjectInfo{}, err
	}
	if isGit && !sameDirectory(root, path) {
		return ProjectInfo{}, errors.New("Git project path must be the repository root")
	}
	return ProjectInfo{Path: path, Git: isGit}, nil
}

func (s *HostService) CreateWindow(ctx context.Context, request CreateWindowRequest) (Window, error) {
	if err := validateID(request.WorkspaceID, "workspace identifier"); err != nil {
		return Window{}, err
	}
	id, err := newID()
	if err != nil {
		return Window{}, err
	}
	now := s.now().UTC()
	var window Window
	var workspacePath string
	if err := s.store.withLock(func(registry *hostRegistry) error {
		var workspace *Workspace
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == request.WorkspaceID {
				workspace = &registry.Workspaces[i]
				break
			}
		}
		if workspace == nil {
			return errors.New("workspace no longer exists")
		}
		if workspace.CreatePending || workspace.DeletePending {
			return errors.New("workspace recovery or deletion is pending; retry cleanup before creating a window")
		}
		if info, err := os.Stat(workspace.Path); err != nil || !info.IsDir() {
			return errors.New("workspace directory is unavailable")
		}
		names := make(map[string]bool)
		for _, existing := range registry.Windows {
			if existing.WorkspaceID == request.WorkspaceID {
				names[existing.Name] = true
			}
		}
		window = Window{
			ID: id, WorkspaceID: request.WorkspaceID, Name: nextDefaultName(defaultWindowName, names),
			Session: s.sessionName(id), CreatedAt: now, LastUsedAt: now, Alive: true, CreatePending: true,
		}
		workspacePath = workspace.Path
		registry.Windows = append(registry.Windows, window)
		return nil
	}); err != nil {
		return Window{}, err
	}
	args := []string{"-f", "/dev/null", "-L", s.socketName(), "start-server"}
	for _, setting := range tmuxGlobalSettings() {
		args = append(args, ";", "set-option", "-g", setting[0], setting[1])
	}
	args = append(args, ";", "new-session", "-d", "-s", window.Session, "-c", workspacePath, "-x", "100", "-y", "30",
		"-e", "MCE_INSTANCE="+s.store.instanceID, "-e", "MCE_WINDOW="+window.ID, "-e", "MCE_WORKSPACE="+window.WorkspaceID)
	if _, err := s.runner.run(ctx, "tmux", args...); err != nil {
		s.rollbackWindowCreation(window)
		return Window{}, errors.New("create tmux session: tmux is unavailable or rejected the session")
	}
	if err := s.configureTmux(ctx); err != nil {
		s.rollbackWindowCreation(window)
		return Window{}, err
	}
	owned, err := s.ownsSession(ctx, window)
	if err != nil || !owned {
		s.rollbackWindowCreation(window)
		return Window{}, errors.New("verify tmux session ownership")
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == window.ID {
				registry.Windows[i].CreatePending = false
				for j := range registry.Workspaces {
					if registry.Workspaces[j].ID == window.WorkspaceID {
						registry.Workspaces[j].LastUsedAt = now
					}
				}
				return nil
			}
		}
		return errors.New("window creation record disappeared")
	}); err != nil {
		s.rollbackWindowCreation(window)
		return Window{}, err
	}
	window.CreatePending = false
	return window, nil
}

func (s *HostService) RenameWorkspace(_ context.Context, request RenameRequest) error {
	if err := validateID(request.ID, "workspace identifier"); err != nil {
		return err
	}
	if err := validateName(request.Name, "workspace name"); err != nil {
		return err
	}
	return s.store.withLock(func(registry *hostRegistry) error {
		index := -1
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == request.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("workspace no longer exists")
		}
		workspace := &registry.Workspaces[index]
		if workspace.CreatePending || workspace.DeletePending {
			return errors.New("workspace recovery or deletion is pending; retry cleanup before renaming it")
		}
		for i, existing := range registry.Workspaces {
			if i != index && existing.ProjectID == workspace.ProjectID && existing.Name == request.Name {
				return errors.New("a workspace with this name already exists in the project")
			}
		}
		workspace.Name = request.Name
		workspace.LastUsedAt = s.now().UTC()
		return nil
	})
}

func (s *HostService) RenameWindow(_ context.Context, request RenameRequest) error {
	if err := validateID(request.ID, "window identifier"); err != nil {
		return err
	}
	if err := validateName(request.Name, "window name"); err != nil {
		return err
	}
	return s.store.withLock(func(registry *hostRegistry) error {
		index := -1
		for i := range registry.Windows {
			if registry.Windows[i].ID == request.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("window no longer exists")
		}
		window := &registry.Windows[index]
		if window.CreatePending || window.DeletePending {
			return errors.New("window recovery or deletion is pending; retry cleanup before renaming it")
		}
		for i, existing := range registry.Windows {
			if i != index && existing.WorkspaceID == window.WorkspaceID && existing.Name == request.Name {
				return errors.New("a window with this name already exists in the workspace")
			}
		}
		window.Name = request.Name
		window.LastUsedAt = s.now().UTC()
		return nil
	})
}

func (s *HostService) rollbackWindowCreation(window Window) {
	rollbackContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, _, err := s.inspectSession(rollbackContext, window)
	if err != nil {
		return
	}
	if state == sessionAltered {
		return
	}
	if state == sessionOwned && s.killOwnedSession(rollbackContext, window) != nil {
		return
	}
	if state == sessionAbsent && s.cleanupUnusedTmuxSocket(rollbackContext) != nil {
		return
	}
	_ = s.store.withLock(func(registry *hostRegistry) error {
		kept := registry.Windows[:0]
		for _, candidate := range registry.Windows {
			if candidate.ID != window.ID {
				kept = append(kept, candidate)
			}
		}
		registry.Windows = kept
		return nil
	})
}

func (s *HostService) PutAttachment(_ context.Context, request PutAttachmentRequest) (AttachmentFile, error) {
	if err := validateID(request.WorkspaceID, "workspace identifier"); err != nil {
		return AttachmentFile{}, err
	}
	if len(request.Data) == 0 || len(request.Data) > maxAttachment {
		return AttachmentFile{}, fmt.Errorf("attachment must be between 1 byte and %d MiB", maxAttachment>>20)
	}
	extension := normalizeExtension(request.Extension)
	if extension == "" {
		extension = ".bin"
	}
	if request.Image {
		config, format, err := image.DecodeConfig(bytes.NewReader(request.Data))
		if err != nil || format != "png" && format != "jpeg" {
			return AttachmentFile{}, errors.New("clipboard image must be a valid PNG or JPEG")
		}
		if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 100_000_000 {
			return AttachmentFile{}, errors.New("clipboard image dimensions are invalid or too large")
		}
		if format == "png" {
			extension = ".png"
		} else {
			extension = ".jpg"
		}
	}
	id, err := newID()
	if err != nil {
		return AttachmentFile{}, err
	}
	attachment := AttachmentFile{ID: id, WorkspaceID: request.WorkspaceID, CreatedAt: s.now().UTC(), CreatePending: true}
	err = s.store.withLock(func(registry *hostRegistry) error {
		workspaceFound := false
		for i, workspace := range registry.Workspaces {
			if workspace.ID == request.WorkspaceID {
				if workspace.CreatePending || workspace.DeletePending {
					return errors.New("workspace recovery or deletion is pending; retry cleanup before adding an attachment")
				}
				workspaceFound = true
				registry.Workspaces[i].LastUsedAt = attachment.CreatedAt
				break
			}
		}
		if !workspaceFound {
			return errors.New("workspace no longer exists")
		}
		paths := []string{
			filepath.Join(s.store.base, "editor"),
			filepath.Join(s.store.base, "editor", "attachments"),
			filepath.Join(s.store.base, "editor", "attachments", s.store.instanceID),
			filepath.Join(s.store.base, "editor", "attachments", s.store.instanceID, request.WorkspaceID),
		}
		for _, path := range paths {
			if err := ensurePrivateDir(path); err != nil {
				return err
			}
		}
		attachment.Path = filepath.Join(paths[len(paths)-1], id+extension)
		registry.Attachments = append(registry.Attachments, attachment)
		return nil
	})
	if err != nil {
		return AttachmentFile{}, err
	}
	file, err := os.OpenFile(attachment.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, errors.New("create private host attachment")
	}
	if _, err := file.Write(request.Data); err != nil {
		file.Close()
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, errors.New("write private host attachment")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, errors.New("sync private host attachment")
	}
	if err := file.Close(); err != nil {
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, errors.New("close private host attachment")
	}
	if err := syncDirectory(filepath.Dir(attachment.Path)); err != nil {
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, errors.New("sync private host attachment directory")
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Attachments {
			if registry.Attachments[i].ID == attachment.ID && registry.Attachments[i].CreatePending {
				registry.Attachments[i].CreatePending = false
				return nil
			}
		}
		return errors.New("attachment creation record disappeared")
	}); err != nil {
		s.rollbackAttachmentCreation(attachment)
		return AttachmentFile{}, err
	}
	attachment.CreatePending = false
	return attachment, nil
}

func (s *HostService) rollbackAttachmentCreation(attachment AttachmentFile) {
	if err := s.removeOwnedAttachment(attachment); err != nil {
		return
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		kept := registry.Attachments[:0]
		for _, candidate := range registry.Attachments {
			if candidate.ID != attachment.ID {
				kept = append(kept, candidate)
			}
		}
		registry.Attachments = kept
		return nil
	}); err == nil {
		s.removeEmptyAttachmentDirs(attachment.WorkspaceID)
	}
}

func (s *HostService) TouchWindow(ctx context.Context, id string) error {
	if err := validateID(id, "window identifier"); err != nil {
		return err
	}
	return s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == id {
				owned, err := s.ownsSession(ctx, registry.Windows[i])
				if err != nil {
					return errors.New("window tmux session state is uncertain")
				}
				if !owned {
					return errors.New("window tmux session is unavailable or not owned by this editor")
				}
				registry.Windows[i].LastUsedAt = s.now().UTC()
				for j := range registry.Workspaces {
					if registry.Workspaces[j].ID == registry.Windows[i].WorkspaceID {
						registry.Workspaces[j].LastUsedAt = registry.Windows[i].LastUsedAt
					}
				}
				return nil
			}
		}
		return errors.New("window no longer exists")
	})
}

func (s *HostService) CopyMode(ctx context.Context, id string) error {
	if err := validateID(id, "window identifier"); err != nil {
		return err
	}
	var selected *Window
	if err := s.store.withReadLock(func(registry hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == id {
				window := registry.Windows[i]
				selected = &window
				return nil
			}
		}
		return errors.New("window no longer exists")
	}); err != nil {
		return err
	}
	owned, err := s.ownsSession(ctx, *selected)
	if err != nil || !owned {
		return errors.New("refuse to control a tmux session without exact editor ownership")
	}
	if _, err := s.tmux(ctx, "copy-mode", "-t", selected.Session); err != nil {
		return errors.New("open tmux scrollback")
	}
	return nil
}

func (s *HostService) DeleteWindow(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	return s.deleteWindow(ctx, request, false)
}

func (s *HostService) deleteWindow(ctx context.Context, request DeleteRequest, recovering bool) (DeleteResult, error) {
	if err := validateID(request.ID, "window identifier"); err != nil {
		return DeleteResult{}, err
	}
	var window Window
	found := false
	if err := s.store.withReadLock(func(registry hostRegistry) error {
		for _, candidate := range registry.Windows {
			if candidate.ID == request.ID {
				window, found = candidate, true
				break
			}
		}
		return nil
	}); err != nil {
		return DeleteResult{}, err
	}
	if !found {
		return DeleteResult{Reason: "window no longer exists"}, nil
	}
	effectiveForce := request.Force
	state, alive, err := s.inspectSession(ctx, window)
	if err != nil {
		return DeleteResult{}, err
	}
	if state == sessionAltered {
		return DeleteResult{}, errors.New("window tmux ownership changed; no deletion was performed")
	}
	if state == sessionOwned && !effectiveForce {
		if alive {
			if !recovering {
				if err := s.clearWindowDeletePending(window.ID); err != nil {
					return DeleteResult{}, err
				}
			}
			return DeleteResult{Reason: "window still has a live process; confirm permanent deletion", Forceable: true}, nil
		}
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == window.ID {
				registry.Windows[i].DeletePending = true
				return nil
			}
		}
		return errors.New("window no longer exists")
	}); err != nil {
		return DeleteResult{}, err
	}
	state, alive, err = s.inspectSession(ctx, window)
	if err != nil {
		return DeleteResult{}, err
	}
	if state == sessionAltered {
		return DeleteResult{}, errors.New("window tmux ownership changed; no deletion was performed")
	}
	if state == sessionOwned && !effectiveForce {
		if alive {
			if !recovering {
				if err := s.clearWindowDeletePending(window.ID); err != nil {
					return DeleteResult{}, err
				}
			}
			return DeleteResult{Reason: "window became active; confirm permanent deletion", Forceable: true}, nil
		}
	}
	if state == sessionOwned {
		if err := s.killOwnedSession(ctx, window); err != nil {
			return DeleteResult{}, err
		}
	} else if err := s.cleanupUnusedTmuxSocket(ctx); err != nil {
		return DeleteResult{}, err
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == window.ID {
				registry.Windows = append(registry.Windows[:i], registry.Windows[i+1:]...)
				return nil
			}
		}
		return nil
	}); err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{Deleted: true}, nil
}

func (s *HostService) DeleteWorkspace(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	return s.deleteWorkspace(ctx, request, false)
}

func (s *HostService) deleteWorkspace(ctx context.Context, request DeleteRequest, recovering bool) (DeleteResult, error) {
	if err := validateID(request.ID, "workspace identifier"); err != nil {
		return DeleteResult{}, err
	}
	var workspace Workspace
	var windows []Window
	found := false
	if err := s.store.withReadLock(func(registry hostRegistry) error {
		for _, window := range registry.Windows {
			if window.WorkspaceID == request.ID {
				windows = append(windows, window)
			}
		}
		for _, candidate := range registry.Workspaces {
			if candidate.ID == request.ID {
				workspace, found = candidate, true
				break
			}
		}
		return nil
	}); err != nil {
		return DeleteResult{}, err
	}
	if !found {
		return DeleteResult{Reason: "workspace no longer exists"}, nil
	}
	effectiveForce := request.Force
	reason, forceable, err := s.workspaceDeletionRisk(ctx, workspace, windows)
	if err != nil {
		return DeleteResult{}, err
	}
	if reason != "" && !effectiveForce {
		if !recovering {
			if err := s.clearWorkspaceCascadeDeletePending(workspace.ID); err != nil {
				return DeleteResult{}, err
			}
		}
		return DeleteResult{Reason: reason, Forceable: forceable}, nil
	}
	if reason != "" && !forceable {
		return DeleteResult{}, errors.New(reason)
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == workspace.ID {
				registry.Workspaces[i].DeletePending = true
				return nil
			}
		}
		return errors.New("workspace no longer exists")
	}); err != nil {
		return DeleteResult{}, err
	}
	for _, window := range windows {
		result, err := s.deleteWindow(ctx, DeleteRequest{ID: window.ID, Force: effectiveForce}, recovering)
		if err != nil {
			return DeleteResult{}, err
		}
		if result.Deleted || result.Reason == "window no longer exists" {
			continue
		}
		if !recovering {
			if clearErr := s.clearWorkspaceCascadeDeletePending(workspace.ID); clearErr != nil {
				return DeleteResult{}, clearErr
			}
		}
		return result, nil
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for _, window := range registry.Windows {
			if window.WorkspaceID == workspace.ID {
				return errors.New("workspace gained a terminal window during deletion; retry")
			}
		}
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == workspace.ID {
				registry.Workspaces[i].DeletePending = true
				return nil
			}
		}
		return errors.New("workspace no longer exists")
	}); err != nil {
		return DeleteResult{}, err
	}
	if workspace.Git {
		if err := s.removeGitWorktree(ctx, workspace, effectiveForce); err != nil {
			return DeleteResult{}, err
		}
	}
	var attachments []AttachmentFile
	if err := s.store.withReadLock(func(registry hostRegistry) error {
		for _, attachment := range registry.Attachments {
			if attachment.WorkspaceID == workspace.ID {
				attachments = append(attachments, attachment)
			}
		}
		return nil
	}); err != nil {
		return DeleteResult{}, err
	}
	for _, attachment := range attachments {
		if err := s.removeOwnedAttachment(attachment); err != nil {
			return DeleteResult{}, err
		}
	}
	if err := s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == workspace.ID {
				registry.Workspaces = append(registry.Workspaces[:i], registry.Workspaces[i+1:]...)
				break
			}
		}
		kept := registry.Attachments[:0]
		for _, attachment := range registry.Attachments {
			if attachment.WorkspaceID != workspace.ID {
				kept = append(kept, attachment)
			}
		}
		registry.Attachments = kept
		return nil
	}); err != nil {
		return DeleteResult{}, err
	}
	s.removeEmptyAttachmentDirs(workspace.ID)
	s.removeEmptyWorktreeDirs(workspace.ProjectID)
	return DeleteResult{Deleted: true}, nil
}

func (s *HostService) workspaceDeletionRisk(ctx context.Context, workspace Workspace, windows []Window) (string, bool, error) {
	var reasons []string
	allForceable := true
	liveWindows := 0
	uncertainWindows := 0
	for _, window := range windows {
		state, alive, err := s.inspectSession(ctx, window)
		if err != nil {
			return "", false, err
		}
		switch {
		case state == sessionAltered:
			uncertainWindows++
			allForceable = false
		case state == sessionOwned && alive:
			liveWindows++
		}
	}
	if uncertainWindows > 0 {
		label := "terminal window has"
		if uncertainWindows != 1 {
			label = "terminal windows have"
		}
		reasons = append(reasons, fmt.Sprintf("%d %s changed or uncertain ownership", uncertainWindows, label))
	}
	if liveWindows > 0 {
		label := "terminal window"
		if liveWindows != 1 {
			label = "terminal windows"
		}
		reasons = append(reasons, fmt.Sprintf("workspace has %d live %s", liveWindows, label))
	}
	if workspace.Git {
		reason, forceable := s.gitWorkspaceDeletionRiskForPath(ctx, workspace)
		if reason != "" {
			reasons = append(reasons, reason)
			allForceable = allForceable && forceable
		}
	}
	if len(reasons) == 0 {
		return "", false, nil
	}
	suffix := "; no deletion was performed"
	if allForceable {
		suffix = "; confirm permanent deletion"
	}
	return strings.Join(reasons, "; ") + suffix, allForceable, nil
}

func (s *HostService) gitWorkspaceDeletionRiskForPath(ctx context.Context, workspace Workspace) (string, bool) {
	if _, err := os.Lstat(workspace.Path); err == nil {
		return s.gitWorkspaceDeletionRisk(ctx, workspace)
	} else if errors.Is(err, os.ErrNotExist) {
		return s.gitBranchDeletionRisk(ctx, workspace)
	}
	return "worktree state is uncertain; no deletion was performed", false
}

func (s *HostService) clearWindowDeletePending(windowID string) error {
	return s.store.withLock(func(registry *hostRegistry) error {
		for i := range registry.Windows {
			if registry.Windows[i].ID == windowID {
				registry.Windows[i].DeletePending = false
				return nil
			}
		}
		return errors.New("window no longer exists")
	})
}

func (s *HostService) clearWorkspaceCascadeDeletePending(workspaceID string) error {
	return s.store.withLock(func(registry *hostRegistry) error {
		found := false
		for i := range registry.Workspaces {
			if registry.Workspaces[i].ID == workspaceID {
				registry.Workspaces[i].DeletePending = false
				found = true
				break
			}
		}
		if !found {
			return errors.New("workspace no longer exists")
		}
		for i := range registry.Windows {
			if registry.Windows[i].WorkspaceID == workspaceID {
				registry.Windows[i].DeletePending = false
			}
		}
		return nil
	})
}

func (s *HostService) Cleanup(ctx context.Context) (CleanupResult, error) {
	result := CleanupResult{}
	cutoff := s.now().UTC().Add(-cleanupAfter)
	recoveryNotes, err := s.reconcilePendingCreates(ctx)
	if err != nil {
		return result, err
	}
	result.Skipped = append(result.Skipped, recoveryNotes...)
	var registry hostRegistry
	if err := s.store.withReadLock(func(current hostRegistry) error { registry = current; return nil }); err != nil {
		return result, err
	}
	for _, window := range registry.Windows {
		if !window.DeletePending && !window.LastUsedAt.Before(cutoff) {
			continue
		}
		if !window.DeletePending {
			owned, err := s.ownsSession(ctx, window)
			if err != nil {
				result.Skipped = append(result.Skipped, window.Name+": tmux state is uncertain")
				continue
			}
			if owned {
				alive, err := s.paneAlive(ctx, window.Session)
				if err != nil {
					result.Skipped = append(result.Skipped, window.Name+": process state is uncertain")
					continue
				}
				if alive {
					continue
				}
			}
		}
		deleted, err := s.deleteWindow(ctx, DeleteRequest{ID: window.ID}, true)
		if err != nil || !deleted.Deleted {
			result.Skipped = append(result.Skipped, window.Name+": safe deletion did not complete")
			continue
		}
		result.WindowsDeleted++
	}
	if err := s.store.withReadLock(func(current hostRegistry) error { registry = current; return nil }); err != nil {
		return result, err
	}
	for _, workspace := range registry.Workspaces {
		if !workspace.DeletePending {
			hasWindow := false
			for _, window := range registry.Windows {
				if window.WorkspaceID == workspace.ID {
					hasWindow = true
					break
				}
			}
			if hasWindow || !workspace.LastUsedAt.Before(cutoff) {
				continue
			}
		}
		deleted, err := s.deleteWorkspace(ctx, DeleteRequest{ID: workspace.ID}, true)
		if err != nil || !deleted.Deleted {
			reason := deleted.Reason
			if reason == "" {
				reason = "safe deletion did not complete"
			}
			result.Skipped = append(result.Skipped, workspace.Name+": "+reason)
			continue
		}
		result.WorkspacesDeleted++
	}
	if err := s.store.withReadLock(func(current hostRegistry) error { registry = current; return nil }); err != nil {
		return result, err
	}
	for _, attachment := range registry.Attachments {
		if attachment.CreatePending {
			result.Skipped = append(result.Skipped, "attachment: pending upload recovery is uncertain")
			continue
		}
		if !attachment.CreatedAt.Before(cutoff) {
			continue
		}
		if err := s.removeOwnedAttachment(attachment); err != nil {
			result.Skipped = append(result.Skipped, "attachment: safe removal failed")
			continue
		}
		if err := s.store.withLock(func(current *hostRegistry) error {
			kept := current.Attachments[:0]
			for _, candidate := range current.Attachments {
				if candidate.ID != attachment.ID {
					kept = append(kept, candidate)
				}
			}
			current.Attachments = kept
			return nil
		}); err != nil {
			return result, err
		}
		s.removeEmptyAttachmentDirs(attachment.WorkspaceID)
		result.AttachmentsDeleted++
	}
	if err := s.cleanupUnusedTmuxSocket(ctx); err != nil {
		result.Skipped = append(result.Skipped, "tmux socket: safe cleanup did not complete")
	}
	return result, nil
}

func (s *HostService) reconcilePendingCreates(ctx context.Context) ([]string, error) {
	var notes []string
	var prunedProjects []string
	var prunedAttachmentWorkspaces []string
	err := s.store.withLock(func(registry *hostRegistry) error {
		keptWorkspaces := registry.Workspaces[:0]
		for i := range registry.Workspaces {
			workspace := registry.Workspaces[i]
			if !workspace.CreatePending {
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			if !workspace.Git {
				workspace.CreatePending = false
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			info, statErr := os.Stat(workspace.Path)
			if statErr == nil && info.IsDir() {
				root, isGit, rootErr := s.gitRoot(ctx, workspace.Path)
				commonDir, commonErr := s.gitCommonDir(ctx, workspace.Path)
				branch, branchErr := s.runner.run(ctx, "git", "-C", workspace.Path, "branch", "--show-current")
				if rootErr == nil && isGit && commonErr == nil && sameDirectory(root, workspace.Path) && sameDirectory(commonDir, workspace.GitCommonDir) && branchErr == nil && strings.TrimSpace(string(branch)) == workspace.Branch {
					workspace.CreatePending = false
					keptWorkspaces = append(keptWorkspaces, workspace)
					continue
				}
				notes = append(notes, workspace.Name+": pending worktree recovery is uncertain")
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				notes = append(notes, workspace.Name+": pending worktree path is uncertain")
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			if err := s.verifyGitProject(ctx, workspace); err != nil {
				notes = append(notes, workspace.Name+": pending Git project identity is uncertain")
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			exists, branchErr := s.gitBranchExists(ctx, workspace.ProjectPath, workspace.Branch)
			if branchErr != nil {
				notes = append(notes, workspace.Name+": pending branch state is uncertain")
				keptWorkspaces = append(keptWorkspaces, workspace)
				continue
			}
			if !exists {
				prunedProjects = append(prunedProjects, workspace.ProjectID)
				continue
			}
			// A branch without its exact worktree does not prove ownership. It can
			// predate a failed `git worktree add -b`, so never delete it.
			notes = append(notes, workspace.Name+": pending branch ownership is uncertain")
			keptWorkspaces = append(keptWorkspaces, workspace)
		}
		registry.Workspaces = keptWorkspaces

		workspaceReady := map[string]bool{}
		for _, workspace := range registry.Workspaces {
			workspaceReady[workspace.ID] = !workspace.CreatePending && !workspace.DeletePending
		}
		keptWindows := registry.Windows[:0]
		for i := range registry.Windows {
			window := registry.Windows[i]
			if !window.CreatePending {
				keptWindows = append(keptWindows, window)
				continue
			}
			if !workspaceReady[window.WorkspaceID] {
				notes = append(notes, window.Name+": parent workspace recovery is pending")
				keptWindows = append(keptWindows, window)
				continue
			}
			state, _, ownershipErr := s.inspectSession(ctx, window)
			if ownershipErr != nil {
				notes = append(notes, window.Name+": pending tmux recovery is uncertain")
				keptWindows = append(keptWindows, window)
				continue
			}
			if state == sessionAltered {
				notes = append(notes, window.Name+": pending tmux ownership changed")
				keptWindows = append(keptWindows, window)
				continue
			}
			if state == sessionAbsent {
				continue
			}
			if err := s.configureTmux(ctx); err != nil {
				notes = append(notes, window.Name+": pending tmux configuration failed")
				keptWindows = append(keptWindows, window)
				continue
			}
			window.CreatePending = false
			keptWindows = append(keptWindows, window)
		}
		registry.Windows = keptWindows

		keptAttachments := registry.Attachments[:0]
		for _, attachment := range registry.Attachments {
			if !attachment.CreatePending {
				keptAttachments = append(keptAttachments, attachment)
				continue
			}
			if err := s.removeOwnedAttachment(attachment); err != nil {
				notes = append(notes, "attachment: pending upload recovery is uncertain")
				keptAttachments = append(keptAttachments, attachment)
			} else {
				prunedAttachmentWorkspaces = append(prunedAttachmentWorkspaces, attachment.WorkspaceID)
			}
		}
		registry.Attachments = keptAttachments
		return nil
	})
	if err == nil {
		for _, projectID := range prunedProjects {
			s.removeEmptyWorktreeDirs(projectID)
		}
		for _, workspaceID := range prunedAttachmentWorkspaces {
			s.removeEmptyAttachmentDirs(workspaceID)
		}
	}
	return notes, err
}

func (s *HostService) gitBranchExists(ctx context.Context, projectPath, branch string) (bool, error) {
	_, err := s.runner.run(ctx, "git", "-C", projectPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var failure commandFailure
	if errors.As(err, &failure) && failure.exitCode == 1 && !failure.notFound {
		return false, nil
	}
	return false, errors.New("inspect owned Git branch")
}

func (s *HostService) Doctor(ctx context.Context) DoctorResult {
	result := DoctorResult{OK: true}
	if version := s.safeVersion(ctx, "tmux", "-V"); version == "" {
		result.OK = false
		result.Issues = append(result.Issues, "tmux is unavailable")
	} else if !supportedTmuxVersion(version) {
		result.OK = false
		result.Issues = append(result.Issues, "tmux 3.2 or newer is required")
	} else {
		result.Checks = append(result.Checks, version)
	}
	if version := s.safeVersion(ctx, "git", "--version"); version == "" {
		result.OK = false
		result.Issues = append(result.Issues, "git is unavailable")
	} else {
		result.Checks = append(result.Checks, version)
	}
	if err := secureExistingDir(s.store.base); err != nil {
		result.OK = false
		result.Issues = append(result.Issues, "multicodex home is not a private regular directory")
	} else if err := secureExistingDir(s.store.editorRoot); err != nil {
		result.OK = false
		result.Issues = append(result.Issues, "editor directory is not private")
	} else if err := secureExistingDir(s.store.root); err != nil {
		result.OK = false
		result.Issues = append(result.Issues, "editor host state is not private")
	} else {
		result.Checks = append(result.Checks, "editor host path policy is valid")
	}
	return result
}

func supportedTmuxVersion(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}
	parts := strings.SplitN(fields[1], ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minorText := strings.TrimRightFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	minor, err := strconv.Atoi(minorText)
	if err != nil {
		return false
	}
	return major > 3 || major == 3 && minor >= 2
}

func (s *HostService) configureTmux(ctx context.Context) error {
	for _, setting := range tmuxGlobalSettings() {
		if _, err := s.tmux(ctx, "set-option", "-g", setting[0], setting[1]); err != nil {
			return errors.New("configure tmux session")
		}
	}
	_, _ = s.tmux(ctx, "unbind-key", "-a")
	keys, err := s.tmux(ctx, "list-keys", "-T", "prefix")
	if err == nil && len(bytes.TrimSpace(keys)) != 0 {
		return errors.New("configure tmux session")
	}
	if err != nil {
		var failure commandFailure
		if !errors.As(err, &failure) || failure.exitCode != 1 || failure.notFound {
			return errors.New("configure tmux session")
		}
	}
	for _, binding := range [][2]string{{"M-Up", "page-up"}, {"M-Down", "page-down"}} {
		if _, err := s.tmux(ctx, "bind-key", "-T", "copy-mode", binding[0], "send-keys", "-X", binding[1]); err != nil {
			return errors.New("configure tmux history keys")
		}
	}
	if _, err := s.tmux(ctx, "set-option", "-s", "extended-keys", "on"); err != nil {
		return errors.New("configure tmux extended keys")
	}
	// The editor always presents itself as xterm-256color. A fixed private
	// server array slot keeps this setting idempotent across new windows.
	if _, err := s.tmux(ctx, "set-option", "-s", "terminal-features[99]", "xterm-256color:extkeys"); err != nil {
		return errors.New("configure tmux extended key recognition")
	}
	return nil
}

func tmuxGlobalSettings() [][2]string {
	return [][2]string{
		{"prefix", "None"},
		{"prefix2", "None"},
		{"status", "off"},
		{"history-limit", strconv.Itoa(historyLimit)},
		{"mode-keys", "emacs"},
		{"remain-on-exit", "on"},
		{"mouse", "on"},
		{"focus-events", "on"},
		{"escape-time", "10"},
		{"set-clipboard", "off"},
		{"allow-rename", "off"},
	}
}

func (s *HostService) tmux(ctx context.Context, args ...string) ([]byte, error) {
	return s.runner.run(ctx, "tmux", append([]string{"-L", s.socketName()}, args...)...)
}

func (s *HostService) ownsSession(ctx context.Context, window Window) (bool, error) {
	state, _, err := s.inspectSession(ctx, window)
	return state == sessionOwned, err
}

type tmuxSessionState uint8

const (
	sessionAbsent tmuxSessionState = iota
	sessionOwned
	sessionAltered
)

func (s *HostService) inspectSession(ctx context.Context, window Window) (tmuxSessionState, bool, error) {
	if window.Session != s.sessionName(window.ID) {
		return sessionAltered, false, nil
	}
	format := "#{session_name}\t#{MCE_INSTANCE}\t#{MCE_WINDOW}\t#{MCE_WORKSPACE}\t#{pane_dead}\t#{session_windows}\t#{window_panes}"
	out, err := s.tmux(ctx, "display-message", "-p", "-t", window.Session, format)
	if err != nil {
		var failure commandFailure
		if errors.As(err, &failure) && failure.exitCode == 1 && !failure.notFound {
			return sessionAbsent, false, nil
		}
		return sessionAbsent, false, errors.New("inspect tmux session ownership")
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 7 {
		return sessionAbsent, false, errors.New("inspect tmux session ownership")
	}
	if fields[0] != window.Session || fields[1] != s.store.instanceID || fields[2] != window.ID || fields[3] != window.WorkspaceID {
		return sessionAltered, false, nil
	}
	if fields[4] != "0" && fields[4] != "1" {
		return sessionAbsent, false, errors.New("inspect tmux process state")
	}
	if fields[5] != "1" || fields[6] != "1" {
		return sessionAltered, false, nil
	}
	return sessionOwned, fields[4] == "0", nil
}

func (s *HostService) paneAlive(ctx context.Context, session string) (bool, error) {
	out, err := s.tmux(ctx, "display-message", "-p", "-t", session, "#{pane_dead}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "0", nil
}

func (s *HostService) killOwnedSession(ctx context.Context, window Window) error {
	owned, err := s.ownsSession(ctx, window)
	if err != nil || !owned {
		return errors.New("refuse to delete a tmux session without exact editor ownership")
	}
	if _, err := s.tmux(ctx, "kill-session", "-t", window.Session); err != nil {
		return errors.New("delete owned tmux session")
	}
	if err := s.cleanupUnusedTmuxSocket(ctx); err != nil {
		return err
	}
	return nil
}

// cleanupUnusedTmuxSocket removes only this editor instance's exact socket,
// and only after tmux confirms that no server remains behind it. tmux leaves
// this socket behind after its last session exits on supported macOS and Linux
// releases.
func (s *HostService) cleanupUnusedTmuxSocket(ctx context.Context) error {
	_, err := s.tmux(ctx, "list-sessions")
	if err == nil {
		return nil
	}
	var failure commandFailure
	if !errors.As(err, &failure) || failure.notFound || failure.exitCode != 1 {
		return errors.New("inspect editor tmux server before socket cleanup")
	}

	path := s.tmuxSocketPath()
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("refuse to clean an unsafe tmux socket directory")
	}
	directoryStat, ok := directoryInfo.Sys().(*syscall.Stat_t)
	if !ok || directoryStat.Uid != uint32(os.Getuid()) {
		return errors.New("refuse to clean an unowned tmux socket directory")
	}

	socketInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || socketInfo.Mode().Type() != os.ModeSocket {
		return errors.New("refuse to remove a non-socket tmux path")
	}
	socketStat, ok := socketInfo.Sys().(*syscall.Stat_t)
	if !ok || socketStat.Uid != uint32(os.Getuid()) {
		return errors.New("refuse to remove an unowned tmux socket")
	}
	connection, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("preserve a live or uncertain editor tmux socket")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		if errors.Is(dialErr, os.ErrNotExist) {
			if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
				return nil
			}
		}
		return errors.New("preserve an uncertain editor tmux socket")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || currentInfo.Mode().Type() != os.ModeSocket {
		return errors.New("editor tmux socket changed during cleanup")
	}
	currentStat, ok := currentInfo.Sys().(*syscall.Stat_t)
	if !ok || currentStat.Uid != socketStat.Uid || currentStat.Dev != socketStat.Dev || currentStat.Ino != socketStat.Ino {
		return errors.New("editor tmux socket changed during cleanup")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("remove unused editor tmux socket")
	}
	return nil
}

func (s *HostService) gitRoot(ctx context.Context, path string) (string, bool, error) {
	out, err := s.runner.run(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	if err == nil {
		root := filepath.Clean(strings.TrimSpace(string(out)))
		if validateAbsolutePath(root, "Git repository root") != nil {
			return "", false, errors.New("Git returned an unsafe repository root")
		}
		return root, true, nil
	}
	if ctx.Err() != nil {
		return "", false, errors.New("inspect Git repository: operation canceled")
	}
	var failure commandFailure
	if errors.As(err, &failure) && failure.notFound {
		return "", false, errors.New("Git is unavailable")
	}
	marked, markerErr := gitMetadataInAncestors(path)
	if markerErr != nil || marked {
		return "", false, errors.New("project looks like a Git repository, but its state is unavailable")
	}
	bare, bareErr := s.runner.run(ctx, "git", "-C", path, "rev-parse", "--is-bare-repository")
	if bareErr == nil && strings.TrimSpace(string(bare)) == "true" {
		return "", false, errors.New("bare Git repositories cannot be editor projects")
	}
	if ctx.Err() != nil {
		return "", false, errors.New("inspect Git repository: operation canceled")
	}
	return "", false, nil
}

func gitMetadataInAncestors(path string) (bool, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		_, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
	}
}

func (s *HostService) prepareGitWorktree(ctx context.Context, request CreateWorkspaceRequest, id string) (string, string, string, error) {
	remote := request.BaseRemote
	if remote == "" {
		remote = "origin"
	}
	branchName := request.BaseBranch
	if branchName == "" {
		out, err := s.runner.run(ctx, "git", "-C", request.ProjectPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if err == nil {
			branchName = strings.TrimPrefix(strings.TrimSpace(string(out)), remote+"/")
		}
	}
	if branchName == "" {
		for _, candidate := range []string{"main", "master"} {
			if _, err := s.runner.run(ctx, "git", "-C", request.ProjectPath, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+candidate); err == nil {
				branchName = candidate
				break
			}
		}
	}
	baseRef := "HEAD"
	if branchName != "" {
		baseRef = "refs/remotes/" + remote + "/" + branchName
		if _, err := s.runner.run(ctx, "git", "-C", request.ProjectPath, "fetch", "--no-tags", remote, branchName); err != nil {
			if _, cachedErr := s.runner.run(ctx, "git", "-C", request.ProjectPath, "show-ref", "--verify", "--quiet", baseRef); cachedErr != nil {
				return "", "", "", errors.New("fetch the selected base branch before worktree creation")
			}
		}
	}
	branch := "multicodex/" + slug(request.Name) + "-" + id[:8]
	exists, err := s.gitBranchExists(ctx, request.ProjectPath, branch)
	if err != nil {
		return "", "", "", err
	}
	if exists {
		return "", "", "", errors.New("generated workspace branch already exists; retry workspace creation")
	}
	paths := []string{
		filepath.Join(s.store.base, "editor"),
		filepath.Join(s.store.base, "editor", "worktrees"),
		s.store.worktreeRoot,
		filepath.Join(s.store.worktreeRoot, request.ProjectID),
	}
	for _, path := range paths {
		if err := ensurePrivateDir(path); err != nil {
			return "", "", "", err
		}
	}
	projectRoot := paths[len(paths)-1]
	worktreePath := filepath.Join(projectRoot, id)
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		return "", "", "", errors.New("worktree destination already exists")
	}
	return branch, baseRef, worktreePath, nil
}

func (s *HostService) addGitWorktree(ctx context.Context, workspace Workspace) error {
	if err := s.verifyGitProject(ctx, workspace); err != nil {
		return err
	}
	if _, err := s.runner.run(ctx, "git", "-c", "core.hooksPath=/dev/null", "-C", workspace.ProjectPath, "worktree", "add", "-b", workspace.Branch, workspace.Path, workspace.BaseRef); err != nil {
		return errors.New("create Git worktree and branch")
	}
	return nil
}

func (s *HostService) gitWorkspaceDeletionRisk(ctx context.Context, workspace Workspace) (string, bool) {
	if s.verifyGitProject(ctx, workspace) != nil {
		return "Git project identity changed or is uncertain", false
	}
	if err := s.verifyGitWorktree(ctx, workspace); err != nil {
		return "worktree path is unavailable; run Git worktree repair manually", false
	}
	out, err := s.runner.run(ctx, "git", "-C", workspace.Path, "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return "worktree status is uncertain", false
	}
	dirty := len(bytes.TrimSpace(out)) > 0
	branchReason, branchForceable := s.gitBranchDeletionRisk(ctx, workspace)
	if branchReason != "" && !branchForceable {
		return branchReason, false
	}
	if dirty && branchReason != "" {
		return "worktree has uncommitted, untracked, or ignored files and branch has commits not present in its base; confirm permanent deletion", true
	}
	if dirty {
		return "worktree has uncommitted, untracked, or ignored files; confirm permanent deletion", true
	}
	return branchReason, branchForceable
}

func (s *HostService) gitBranchDeletionRisk(ctx context.Context, workspace Workspace) (string, bool) {
	if s.verifyGitProject(ctx, workspace) != nil {
		return "Git project identity changed or is uncertain", false
	}
	exists, err := s.gitBranchExists(ctx, workspace.ProjectPath, workspace.Branch)
	if err != nil {
		return "branch integration state is uncertain", false
	}
	if !exists {
		return "", false
	}
	oid, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "rev-parse", "--verify", "refs/heads/"+workspace.Branch)
	if err != nil || !gitOIDPattern.MatchString(strings.TrimSpace(string(oid))) {
		return "branch integration state is uncertain", false
	}
	out, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "rev-list", "--count", workspace.BaseRef+".."+strings.TrimSpace(string(oid)))
	if err != nil {
		return "branch integration state is uncertain", false
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return "branch integration state is uncertain", false
	}
	if count > 0 {
		return "branch has commits not present in its base; confirm permanent deletion", true
	}
	return "", false
}

func (s *HostService) removeGitWorktree(ctx context.Context, workspace Workspace, force bool) error {
	if err := s.verifyGitProject(ctx, workspace); err != nil {
		return err
	}
	if _, err := os.Lstat(workspace.Path); err == nil {
		if err := s.verifyGitWorktree(ctx, workspace); err != nil {
			return err
		}
		args := []string{"-c", "core.hooksPath=/dev/null", "-C", workspace.ProjectPath, "worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		args = append(args, workspace.Path)
		if _, err := s.runner.run(ctx, "git", args...); err != nil {
			return errors.New("remove owned Git worktree")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect owned Git worktree before removal")
	} else {
		registeredPath, registered, err := s.registeredOwnedWorktree(ctx, workspace)
		if err != nil {
			return err
		}
		if registered {
			args := []string{"-c", "core.hooksPath=/dev/null", "-C", workspace.ProjectPath, "worktree", "remove"}
			if force {
				args = append(args, "--force")
			}
			args = append(args, registeredPath)
			if _, err := s.runner.run(ctx, "git", args...); err != nil {
				return errors.New("remove registered owned Git worktree")
			}
		}
	}
	exists, err := s.gitBranchExists(ctx, workspace.ProjectPath, workspace.Branch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	oid, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "rev-parse", "--verify", "refs/heads/"+workspace.Branch)
	if err != nil || !gitOIDPattern.MatchString(strings.TrimSpace(string(oid))) {
		return errors.New("inspect owned Git branch before removal")
	}
	expectedOID := strings.TrimSpace(string(oid))
	if !force {
		out, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "rev-list", "--count", workspace.BaseRef+".."+expectedOID)
		count, parseErr := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil || parseErr != nil || count != 0 {
			return errors.New("refuse to remove an owned Git branch with new or uncertain commits")
		}
	}
	if _, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "update-ref", "-d", "refs/heads/"+workspace.Branch, expectedOID); err != nil {
		return errors.New("remove owned Git branch after worktree removal")
	}
	return nil
}

func (s *HostService) registeredOwnedWorktree(ctx context.Context, workspace Workspace) (string, bool, error) {
	out, err := s.runner.run(ctx, "git", "-C", workspace.ProjectPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", false, errors.New("inspect registered Git worktrees before removal")
	}
	expectedPath, err := canonicalMissingPath(workspace.Path)
	if err != nil {
		return "", false, errors.New("inspect owned Git worktree path before removal")
	}
	expectedBranch := "refs/heads/" + workspace.Branch
	matched := ""
	for _, record := range bytes.Split(out, []byte{0, 0}) {
		path, branch := "", ""
		for _, field := range bytes.Split(record, []byte{0}) {
			value := string(field)
			switch {
			case strings.HasPrefix(value, "worktree "):
				path = strings.TrimPrefix(value, "worktree ")
			case strings.HasPrefix(value, "branch "):
				branch = strings.TrimPrefix(value, "branch ")
			}
		}
		if path == "" {
			continue
		}
		canonicalPath, pathErr := canonicalMissingPath(path)
		pathMatches := pathErr == nil && canonicalPath == expectedPath
		branchMatches := branch == expectedBranch
		if pathMatches != branchMatches {
			return "", false, errors.New("refuse Git removal because registered worktree ownership is uncertain")
		}
		if pathMatches {
			if matched != "" {
				return "", false, errors.New("refuse Git removal because registered worktree ownership is ambiguous")
			}
			matched = path
		}
	}
	return matched, matched != "", nil
}

func canonicalMissingPath(path string) (string, error) {
	if validateAbsolutePath(path, "Git worktree path") != nil {
		return "", errors.New("invalid Git worktree path")
	}
	current := filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{resolved}, missing...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}

func (s *HostService) gitCommonDir(ctx context.Context, path string) (string, error) {
	out, err := s.runner.run(ctx, "git", "-C", path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", errors.New("inspect Git repository identity")
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(path, commonDir)
	}
	commonDir, err = filepath.EvalSymlinks(filepath.Clean(commonDir))
	if err != nil || validateAbsolutePath(commonDir, "Git common directory") != nil {
		return "", errors.New("inspect Git repository identity")
	}
	info, err := os.Stat(commonDir)
	if err != nil || !info.IsDir() {
		return "", errors.New("Git repository identity is unavailable")
	}
	return commonDir, nil
}

func (s *HostService) verifyGitProject(ctx context.Context, workspace Workspace) error {
	commonDir, err := s.gitCommonDir(ctx, workspace.ProjectPath)
	if err != nil || workspace.GitCommonDir == "" || !sameDirectory(commonDir, workspace.GitCommonDir) {
		return errors.New("refuse Git operation because the project identity changed or is uncertain")
	}
	return nil
}

func (s *HostService) verifyGitWorktree(ctx context.Context, workspace Workspace) error {
	info, err := os.Lstat(workspace.Path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse Git operation because the owned worktree path changed or is uncertain")
	}
	root, isGit, err := s.gitRoot(ctx, workspace.Path)
	if err != nil || !isGit || !sameDirectory(root, workspace.Path) {
		return errors.New("refuse Git operation because the owned worktree identity changed or is uncertain")
	}
	commonDir, err := s.gitCommonDir(ctx, workspace.Path)
	if err != nil || !sameDirectory(commonDir, workspace.GitCommonDir) {
		return errors.New("refuse Git operation because the owned worktree identity changed or is uncertain")
	}
	branch, err := s.runner.run(ctx, "git", "-C", workspace.Path, "branch", "--show-current")
	if err != nil || strings.TrimSpace(string(branch)) != workspace.Branch {
		return errors.New("refuse Git operation because the owned worktree branch changed or is uncertain")
	}
	return nil
}

func (s *HostService) safeVersion(ctx context.Context, command string, args ...string) string {
	out, err := s.runner.run(ctx, command, args...)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if len(line) > 100 {
		line = line[:100]
	}
	return line
}

func safeGitRefPart(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func normalizeExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "." + value
	}
	if len(value) > 17 {
		return ""
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return value
}

func (s *HostService) removeOwnedAttachment(attachment AttachmentFile) error {
	if err := validateID(attachment.ID, "attachment identifier"); err != nil {
		return errors.New("refuse to delete attachment with invalid ownership metadata")
	}
	if err := validateID(attachment.WorkspaceID, "workspace identifier"); err != nil {
		return errors.New("refuse to delete attachment with invalid ownership metadata")
	}
	root := filepath.Join(s.store.base, "editor", "attachments", s.store.instanceID, attachment.WorkspaceID)
	rel, err := filepath.Rel(root, attachment.Path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("refuse to delete attachment outside the owned attachment directory")
	}
	info, err := os.Lstat(attachment.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse to delete attachment that is not an owned regular file")
	}
	if err := os.Remove(attachment.Path); err != nil {
		return errors.New("delete owned attachment")
	}
	if err := syncDirectory(root); err != nil {
		return errors.New("sync owned attachment deletion")
	}
	return nil
}

func (s *HostService) removeEmptyAttachmentDirs(workspaceID string) {
	if validateID(workspaceID, "workspace identifier") != nil {
		return
	}
	instanceRoot := filepath.Join(s.store.base, "editor", "attachments", s.store.instanceID)
	_ = os.Remove(filepath.Join(instanceRoot, workspaceID))
	_ = os.Remove(instanceRoot)
}

func (s *HostService) removeEmptyWorktreeDirs(projectID string) {
	if validateID(projectID, "project identifier") != nil {
		return
	}
	instanceRoot := s.store.worktreeRoot
	_ = os.Remove(filepath.Join(instanceRoot, projectID))
	_ = os.Remove(instanceRoot)
}

func sameDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
