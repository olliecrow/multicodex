package editor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type hostRegistry struct {
	Version     int              `json:"version"`
	InstanceID  string           `json:"instance_id"`
	Workspaces  []Workspace      `json:"workspaces"`
	Windows     []Window         `json:"windows"`
	Attachments []AttachmentFile `json:"attachments,omitempty"`
}

type hostStore struct {
	base          string
	editorRoot    string
	root          string
	registryPath  string
	lockPath      string
	lifecyclePath string
	worktreeRoot  string
	instanceID    string
}

func newHostStore(multicodexHome, instanceID string) (*hostStore, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	editorRoot := filepath.Join(multicodexHome, "editor")
	root := filepath.Join(editorRoot, "host")
	return &hostStore{
		base:          multicodexHome,
		editorRoot:    editorRoot,
		root:          root,
		registryPath:  filepath.Join(root, instanceID+".json"),
		lockPath:      filepath.Join(root, instanceID+".lock"),
		lifecyclePath: filepath.Join(root, "lifecycle.lock"),
		worktreeRoot:  filepath.Join(multicodexHome, "editor", "worktrees", instanceID),
		instanceID:    instanceID,
	}, nil
}

func (s *hostStore) withLifecycleLock(ctx context.Context, fn func() error) error {
	lock, err := s.openLock(s.lifecyclePath, "operation")
	if err != nil {
		return err
	}
	defer lock.Close()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errors.New("lock editor host operation")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func (s *hostStore) withLock(fn func(*hostRegistry) error) error {
	lock, err := s.openLock(s.lockPath, "state")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock editor host state: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck

	registry, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&registry); err != nil {
		return err
	}
	return s.save(registry)
}

func (s *hostStore) withReadLock(fn func(hostRegistry) error) error {
	lock, err := s.openLock(s.lockPath, "state")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return fmt.Errorf("lock editor host state: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	registry, err := s.load()
	if err != nil {
		return err
	}
	return fn(registry)
}

func (s *hostStore) openLock(path, kind string) (*os.File, error) {
	if err := ensurePrivateDir(s.base); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(s.editorRoot); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(s.root); err != nil {
		return nil, err
	}
	if err := secureExistingFile(path); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open editor host %s lock: %w", kind, err)
	}
	if err := lock.Chmod(0o600); err != nil {
		lock.Close()
		return nil, fmt.Errorf("secure editor host %s lock: %w", kind, err)
	}
	return lock, nil
}

func (s *hostStore) load() (hostRegistry, error) {
	if err := secureExistingFile(s.registryPath); err != nil {
		return hostRegistry{}, err
	}
	b, err := readBoundedStateFile(s.registryPath, maxHostState)
	if errors.Is(err, os.ErrNotExist) {
		return hostRegistry{Version: stateVersion, InstanceID: s.instanceID}, nil
	}
	if err != nil {
		return hostRegistry{}, fmt.Errorf("read editor host state: %w", err)
	}
	var registry hostRegistry
	if err := json.Unmarshal(b, &registry); err != nil {
		return hostRegistry{}, fmt.Errorf("parse editor host state: %w", err)
	}
	if registry.Version != stateVersion || registry.InstanceID != s.instanceID {
		return hostRegistry{}, errors.New("editor host state has an unsupported version or owner")
	}
	if err := s.validateRegistry(registry); err != nil {
		return hostRegistry{}, err
	}
	return registry, nil
}

func (s *hostStore) validateRegistry(registry hostRegistry) error {
	if len(registry.Workspaces) > maxStateRecords || len(registry.Windows) > maxStateRecords || len(registry.Attachments) > maxStateRecords {
		return errors.New("editor host state contains too many records")
	}
	workspaceIDs := make(map[string]bool, len(registry.Workspaces))
	externalWorkspaces := make(map[string]bool, len(registry.Workspaces))
	nonGitProjects := make(map[string]bool)
	workspaceNames := make(map[string]bool)
	for _, workspace := range registry.Workspaces {
		if err := validateID(workspace.ID, "workspace identifier"); err != nil {
			return errors.New("editor host state contains invalid workspace ownership metadata")
		}
		if workspaceIDs[workspace.ID] {
			return errors.New("editor host state contains a duplicate workspace identifier")
		}
		workspaceIDs[workspace.ID] = true
		externalWorkspaces[workspace.ID] = workspace.External
		if err := validateID(workspace.ProjectID, "project identifier"); err != nil {
			return errors.New("editor host state contains invalid workspace ownership metadata")
		}
		if err := validateName(workspace.Name, "workspace name"); err != nil {
			return errors.New("editor host state contains invalid workspace ownership metadata")
		}
		workspaceName := workspace.ProjectID + "\x00" + workspace.Name
		if workspaceNames[workspaceName] {
			return errors.New("editor host state contains a duplicate workspace name")
		}
		workspaceNames[workspaceName] = true
		if err := validateAbsolutePath(workspace.ProjectPath, "project path"); err != nil {
			return errors.New("editor host state contains invalid workspace ownership metadata")
		}
		if workspace.Git {
			if validateAbsolutePath(workspace.GitCommonDir, "Git common directory") != nil {
				return errors.New("editor host state contains invalid Git workspace metadata")
			}
			if workspace.External {
				if workspace.Path != workspace.ProjectPath || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
					return errors.New("editor host state contains invalid preserved Git workspace metadata")
				}
			} else {
				expectedPath := filepath.Join(s.worktreeRoot, workspace.ProjectID, workspace.ID)
				if workspace.Path != expectedPath || !validOwnedBranch(workspace.Branch, workspace.ID) || !safeStoredBaseRef(workspace.BaseRef) {
					return errors.New("editor host state contains a Git workspace outside its exact owned path")
				}
			}
		} else {
			if workspace.Path != workspace.ProjectPath || workspace.GitCommonDir != "" || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked || nonGitProjects[workspace.ProjectID] {
				return errors.New("editor host state contains invalid non-Git workspace ownership metadata")
			}
			nonGitProjects[workspace.ProjectID] = true
		}
		if workspace.Unavailable {
			return errors.New("editor host state contains transient workspace metadata")
		}
		if workspace.CreatePending && workspace.DeletePending {
			return errors.New("editor host state contains invalid workspace deletion metadata")
		}
		if workspace.CreatedAt.IsZero() || workspace.LastUsedAt.IsZero() {
			return errors.New("editor host state contains invalid workspace timestamps")
		}
	}

	windowIDs := make(map[string]bool, len(registry.Windows))
	windowNames := make(map[string]bool)
	projectWindows := make(map[string]bool)
	adoptedSessions := make(map[string]bool)
	for _, window := range registry.Windows {
		workspaceWindow := window.WorkspaceID != "" && window.ProjectID == "" && window.ProjectPath == "" && workspaceIDs[window.WorkspaceID]
		projectWindow := window.WorkspaceID == "" && validateID(window.ProjectID, "project identifier") == nil && validateAbsolutePath(window.ProjectPath, "project path") == nil && !projectWindows[window.ProjectID]
		if err := validateID(window.ID, "window identifier"); err != nil || windowIDs[window.ID] || !workspaceWindow && !projectWindow {
			return errors.New("editor host state contains invalid window ownership metadata")
		}
		windowIDs[window.ID] = true
		if projectWindow {
			if window.Name != projectWindowName || window.Adopted {
				return errors.New("editor host state contains invalid project terminal metadata")
			}
			projectWindows[window.ProjectID] = true
		}
		if err := validateName(window.Name, "window name"); err != nil {
			return errors.New("editor host state contains invalid window ownership metadata")
		}
		windowName := windowTargetID(window) + "\x00" + window.Name
		if windowNames[windowName] {
			return errors.New("editor host state contains a duplicate window name")
		}
		windowNames[windowName] = true
		if window.Adopted {
			if !externalWorkspaces[window.WorkspaceID] || validateTmuxSessionName(window.Session) != nil || !tmuxIDPattern.MatchString(window.TmuxSessionID) || adoptedSessions[window.Session] || adoptedSessions[window.TmuxSessionID] {
				return errors.New("editor host state contains invalid adopted tmux metadata")
			}
			adoptedSessions[window.Session] = true
			adoptedSessions[window.TmuxSessionID] = true
		} else if window.Session != "mce-"+window.ID || window.TmuxSessionID != "" {
			return errors.New("editor host state contains invalid window ownership metadata")
		}
		if window.CreatePending && window.DeletePending {
			return errors.New("editor host state contains invalid window deletion metadata")
		}
		if window.CreatedAt.IsZero() || window.LastUsedAt.IsZero() {
			return errors.New("editor host state contains invalid window timestamps")
		}
	}

	attachmentIDs := make(map[string]bool, len(registry.Attachments))
	for _, attachment := range registry.Attachments {
		workspaceTarget := attachment.WorkspaceID != "" && attachment.ProjectID == "" && workspaceIDs[attachment.WorkspaceID]
		projectTarget := attachment.WorkspaceID == "" && attachment.ProjectID != "" && projectWindows[attachment.ProjectID]
		if err := validateID(attachment.ID, "attachment identifier"); err != nil || attachmentIDs[attachment.ID] || !workspaceTarget && !projectTarget {
			return errors.New("editor host state contains invalid attachment ownership metadata")
		}
		attachmentIDs[attachment.ID] = true
		root := filepath.Join(s.base, "editor", "attachments", s.instanceID, attachmentTargetID(attachment))
		base := filepath.Base(attachment.Path)
		extension := strings.TrimPrefix(base, attachment.ID)
		if filepath.Dir(attachment.Path) != root || base != attachment.ID+extension || extension == "" || normalizeExtension(extension) != extension {
			return errors.New("editor host state contains an attachment outside its exact owned path")
		}
		if attachment.CreatedAt.IsZero() {
			return errors.New("editor host state contains an invalid attachment timestamp")
		}
	}
	return nil
}

func safeStoredBaseRef(value string) bool {
	if value == "HEAD" {
		return true
	}
	const prefix = "refs/remotes/"
	return strings.HasPrefix(value, prefix) && safeGitRefPart(strings.TrimPrefix(value, prefix))
}

func (s *hostStore) save(registry hostRegistry) error {
	if registry.Version != stateVersion || registry.InstanceID != s.instanceID {
		return errors.New("refuse to save editor host state with a different owner")
	}
	if err := s.validateRegistry(registry); err != nil {
		return err
	}
	b, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode editor host state: %w", err)
	}
	if len(b)+1 > maxHostState {
		return errors.New("editor host state exceeds its safety limit")
	}
	tmp, err := os.CreateTemp(s.root, ".host-state.*")
	if err != nil {
		return fmt.Errorf("create editor host state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure editor host state: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write editor host state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync editor host state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close editor host state: %w", err)
	}
	if err := os.Rename(tmpPath, s.registryPath); err != nil {
		return fmt.Errorf("replace editor host state: %w", err)
	}
	return syncDirectory(s.root)
}
