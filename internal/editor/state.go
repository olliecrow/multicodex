package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type StateStore struct {
	base string
	root string
	path string
}

func NewStateStore(multicodexHome string) *StateStore {
	root := filepath.Join(multicodexHome, "editor")
	return &StateStore{base: multicodexHome, root: root, path: filepath.Join(root, "state.json")}
}

func (s *StateStore) LoadOrCreate() (ClientState, error) {
	state, err := s.Load()
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ClientState{}, err
	}
	state, err = NewClientState()
	if err != nil {
		return ClientState{}, err
	}
	if err := s.Save(state); err != nil {
		return ClientState{}, err
	}
	return state, nil
}

func (s *StateStore) Load() (ClientState, error) {
	if err := secureExistingDir(s.base); err != nil {
		return ClientState{}, err
	}
	if err := secureExistingDir(s.root); err != nil {
		return ClientState{}, err
	}
	if err := secureExistingFile(s.path); err != nil {
		return ClientState{}, err
	}
	b, err := readBoundedStateFile(s.path, maxClientState)
	if err != nil {
		return ClientState{}, err
	}
	var state ClientState
	if err := json.Unmarshal(b, &state); err != nil {
		return ClientState{}, fmt.Errorf("parse editor state: %w", err)
	}
	if err := validateClientState(state); err != nil {
		return ClientState{}, err
	}
	return state, nil
}

func (s *StateStore) Save(state ClientState) error {
	if err := validateClientState(state); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.base); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.root); err != nil {
		return err
	}
	if err := secureExistingFile(s.path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode editor state: %w", err)
	}
	if len(b)+1 > maxClientState {
		return errors.New("editor state exceeds its safety limit")
	}
	tmp, err := os.CreateTemp(s.root, ".state.*")
	if err != nil {
		return fmt.Errorf("create editor state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure editor state: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write editor state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync editor state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close editor state: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace editor state: %w", err)
	}
	return syncDirectory(s.root)
}

func (s *StateStore) AcquireInstanceLock() (*os.File, error) {
	if err := ensurePrivateDir(s.base); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(s.root); err != nil {
		return nil, err
	}
	path := filepath.Join(s.root, "editor.lock")
	if err := secureExistingFile(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.New("open editor instance lock")
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, errors.New("secure editor instance lock")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another multicodex editor is already using this client state")
	}
	return file, nil
}

func releaseInstanceLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("open editor state directory for sync")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("sync editor state directory")
	}
	return nil
}

func validateClientState(state ClientState) error {
	if state.Version != stateVersion {
		return fmt.Errorf("unsupported editor state version %d", state.Version)
	}
	if err := validateID(state.InstanceID, "editor instance identifier"); err != nil {
		return err
	}
	if len(state.Hosts) > maxStateRecords {
		return errors.New("editor state contains too many records")
	}
	hostIDs := map[string]bool{}
	hostNames := map[string]bool{}
	sshAliases := map[string]bool{}
	projectIDs := map[string]bool{}
	projectCount := 0
	for _, host := range state.Hosts {
		if host.ID != localHostID {
			if err := validateID(host.ID, "host identifier"); err != nil {
				return err
			}
		}
		if hostIDs[host.ID] {
			return fmt.Errorf("duplicate host identifier %q", host.ID)
		}
		hostIDs[host.ID] = true
		if err := validateName(host.Name, "host name"); err != nil {
			return err
		}
		if hostNames[host.Name] {
			return errors.New("duplicate host name")
		}
		hostNames[host.Name] = true
		if host.ID == localHostID {
			if host.SSHAlias != "" {
				return errors.New("local host cannot have an SSH alias")
			}
		} else if err := validateSSHAlias(host.SSHAlias); err != nil {
			return err
		} else if sshAliases[host.SSHAlias] {
			return errors.New("duplicate SSH host alias")
		} else {
			sshAliases[host.SSHAlias] = true
		}
		projectNames := map[string]bool{}
		projectPaths := map[string]bool{}
		projectCount += len(host.Projects)
		if projectCount > maxStateRecords {
			return errors.New("editor state contains too many records")
		}
		for _, project := range host.Projects {
			if err := validateID(project.ID, "project identifier"); err != nil {
				return err
			}
			if projectIDs[project.ID] {
				return fmt.Errorf("duplicate project identifier %q", project.ID)
			}
			projectIDs[project.ID] = true
			if err := validateName(project.Name, "project name"); err != nil {
				return err
			}
			if projectNames[project.Name] {
				return errors.New("duplicate project name on one host")
			}
			projectNames[project.Name] = true
			if err := validateRemotePath(project.Path); err != nil {
				return err
			}
			if projectPaths[project.Path] {
				return errors.New("duplicate project path on one host")
			}
			projectPaths[project.Path] = true
		}
	}
	if !hostIDs[localHostID] {
		return errors.New("editor state is missing the local host")
	}
	if state.SelectedWindowID != "" && validateID(state.SelectedWindowID, "selected window identifier") != nil {
		return errors.New("editor state contains an invalid selected window")
	}
	return nil
}

func readBoundedStateFile(path string, limit int) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("editor state exceeds its safety limit")
	}
	return data, nil
}

func ensurePrivateDir(path string) error {
	if err := secureExistingDir(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure private directory: %w", err)
	}
	return nil
}

func secureExistingDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect editor directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("editor directory must be a regular directory, not a link")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("editor directory permissions are too open; expected 0700")
	}
	return nil
}

func secureExistingFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect editor state: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("editor state must be a regular file, not a link")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("editor state permissions are too open; expected 0600")
	}
	return nil
}
