package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type hostRegistry struct {
	Version     int              `json:"version"`
	InstanceID  string           `json:"instance_id"`
	Workspaces  []Workspace      `json:"workspaces"`
	Windows     []Window         `json:"windows"`
	Attachments []AttachmentFile `json:"attachments,omitempty"`
}

type hostStore struct {
	base         string
	editorRoot   string
	root         string
	registryPath string
	lockPath     string
	worktreeRoot string
	instanceID   string
}

func newHostStore(multicodexHome, instanceID string) (*hostStore, error) {
	if err := validateID(instanceID, "editor instance identifier"); err != nil {
		return nil, err
	}
	editorRoot := filepath.Join(multicodexHome, "editor")
	root := filepath.Join(editorRoot, "host")
	return &hostStore{
		base:         multicodexHome,
		editorRoot:   editorRoot,
		root:         root,
		registryPath: filepath.Join(root, instanceID+".json"),
		lockPath:     filepath.Join(root, instanceID+".lock"),
		worktreeRoot: filepath.Join(multicodexHome, "editor", "worktrees", instanceID),
		instanceID:   instanceID,
	}, nil
}

func (s *hostStore) withLock(fn func(*hostRegistry) error) error {
	if err := ensurePrivateDir(s.base); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.editorRoot); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.root); err != nil {
		return err
	}
	if err := secureExistingFile(s.lockPath); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open editor host lock: %w", err)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("secure editor host lock: %w", err)
	}
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
	if err := ensurePrivateDir(s.base); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.editorRoot); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.root); err != nil {
		return err
	}
	if err := secureExistingFile(s.lockPath); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open editor host lock: %w", err)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("secure editor host lock: %w", err)
	}
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

func (s *hostStore) load() (hostRegistry, error) {
	if err := secureExistingFile(s.registryPath); err != nil {
		return hostRegistry{}, err
	}
	b, err := os.ReadFile(s.registryPath)
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
	workspaceIDs := make(map[string]bool, len(registry.Workspaces))
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
			expectedPath := filepath.Join(s.worktreeRoot, workspace.ProjectID, workspace.ID)
			if workspace.Path != expectedPath || validateAbsolutePath(workspace.GitCommonDir, "Git common directory") != nil || !validOwnedBranch(workspace.Branch, workspace.ID) || !safeStoredBaseRef(workspace.BaseRef) {
				return errors.New("editor host state contains a Git workspace outside its exact owned path")
			}
		} else {
			if workspace.Path != workspace.ProjectPath || workspace.GitCommonDir != "" || workspace.Branch != "" || workspace.BaseRef != "" || nonGitProjects[workspace.ProjectID] {
				return errors.New("editor host state contains invalid non-Git workspace ownership metadata")
			}
			nonGitProjects[workspace.ProjectID] = true
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
	for _, window := range registry.Windows {
		if err := validateID(window.ID, "window identifier"); err != nil || windowIDs[window.ID] || !workspaceIDs[window.WorkspaceID] {
			return errors.New("editor host state contains invalid window ownership metadata")
		}
		windowIDs[window.ID] = true
		if err := validateName(window.Name, "window name"); err != nil {
			return errors.New("editor host state contains invalid window ownership metadata")
		}
		windowName := window.WorkspaceID + "\x00" + window.Name
		if windowNames[windowName] {
			return errors.New("editor host state contains a duplicate window name")
		}
		windowNames[windowName] = true
		if window.Session != "mce-"+window.ID {
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
		if err := validateID(attachment.ID, "attachment identifier"); err != nil || attachmentIDs[attachment.ID] || !workspaceIDs[attachment.WorkspaceID] {
			return errors.New("editor host state contains invalid attachment ownership metadata")
		}
		attachmentIDs[attachment.ID] = true
		root := filepath.Join(s.base, "editor", "attachments", s.instanceID, attachment.WorkspaceID)
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
