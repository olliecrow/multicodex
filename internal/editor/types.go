package editor

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	stateVersion    = 1
	hostProtocol    = 1
	historyLimit    = 50000
	activityRows    = 100
	cleanupAfter    = 7 * 24 * time.Hour
	maxAttachment   = 16 << 20
	minimumWidth    = 80
	minimumHeight   = 24
	localHostID     = "local"
	localHostName   = "Local"
	defaultShellWin = "Terminal"
)

var (
	idPattern       = regexp.MustCompile(`^[a-f0-9]{24}$`)
	paneHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	gitOIDPattern   = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
	sshAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type ClientState struct {
	Version          int        `json:"version"`
	InstanceID       string     `json:"instance_id"`
	Hosts            []Host     `json:"hosts"`
	SelectedWindowID string     `json:"selected_window_id,omitempty"`
	Activities       []Activity `json:"activities,omitempty"`
}

type Host struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	SSHAlias string    `json:"ssh_alias,omitempty"`
	Projects []Project `json:"projects,omitempty"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Activity struct {
	HostID    string    `json:"host_id"`
	WindowID  string    `json:"window_id"`
	PaneHash  string    `json:"pane_hash,omitempty"`
	ChangedAt time.Time `json:"changed_at"`
}

type HostSnapshot struct {
	Protocol   int         `json:"protocol"`
	Workspaces []Workspace `json:"workspaces"`
	Windows    []Window    `json:"windows"`
}

type Workspace struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ProjectPath   string    `json:"project_path"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Git           bool      `json:"git"`
	GitCommonDir  string    `json:"git_common_dir,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	BaseRef       string    `json:"base_ref,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
	CreatePending bool      `json:"create_pending,omitempty"`
	DeletePending bool      `json:"delete_pending,omitempty"`
}

type Window struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	Session       string    `json:"session"`
	Launch        string    `json:"launch"`
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
	PaneHash      string    `json:"pane_hash,omitempty"`
	Alive         bool      `json:"alive"`
	CreatePending bool      `json:"create_pending,omitempty"`
	DeletePending bool      `json:"delete_pending,omitempty"`
}

type CreateWorkspaceRequest struct {
	ProjectID   string `json:"project_id"`
	ProjectPath string `json:"project_path"`
	Name        string `json:"name"`
	BaseRemote  string `json:"base_remote,omitempty"`
	BaseBranch  string `json:"base_branch,omitempty"`
}

type CreateWindowRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Launch      string `json:"launch"`
}

type DeleteRequest struct {
	ID    string `json:"id"`
	Force bool   `json:"force,omitempty"`
}

type DeleteResult struct {
	Deleted   bool   `json:"deleted"`
	Reason    string `json:"reason,omitempty"`
	Forceable bool   `json:"forceable,omitempty"`
}

type CleanupResult struct {
	WindowsDeleted     int      `json:"windows_deleted"`
	WorkspacesDeleted  int      `json:"workspaces_deleted"`
	AttachmentsDeleted int      `json:"attachments_deleted"`
	Skipped            []string `json:"skipped,omitempty"`
}

type DoctorResult struct {
	OK     bool     `json:"ok"`
	Checks []string `json:"checks"`
	Issues []string `json:"issues,omitempty"`
}

type AttachmentFile struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Path          string    `json:"path"`
	CreatedAt     time.Time `json:"created_at"`
	CreatePending bool      `json:"create_pending,omitempty"`
}

type PutAttachmentRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Extension   string `json:"extension,omitempty"`
	Data        []byte `json:"data"`
	Image       bool   `json:"image,omitempty"`
}

type ProjectInfo struct {
	Path string `json:"path"`
	Git  bool   `json:"git"`
}

func NewClientState() (ClientState, error) {
	id, err := newID()
	if err != nil {
		return ClientState{}, err
	}
	return ClientState{
		Version:    stateVersion,
		InstanceID: id,
		Hosts: []Host{{
			ID:   localHostID,
			Name: localHostName,
		}},
	}, nil
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("create identifier: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func validateID(value, field string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("invalid %s", field)
	}
	return nil
}

func validateName(value, field string) error {
	if value != strings.TrimSpace(value) || value == "" {
		return fmt.Errorf("%s must not be empty or have outer whitespace", field)
	}
	if len([]rune(value)) > 80 {
		return fmt.Errorf("%s is longer than 80 characters", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func validateSSHAlias(value string) error {
	if !sshAliasPattern.MatchString(value) {
		return errors.New("SSH host must be a configured alias containing only letters, numbers, dot, underscore, or hyphen")
	}
	return nil
}

func validateAbsolutePath(value, field string) error {
	if value == "" || len(value) > 4096 || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("%s must be a clean absolute path", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func validateRemotePath(value string) error {
	return validateAbsolutePath(value, "remote path")
}

func slug(value string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			lastDash = false
		} else if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "workspace"
	}
	if len(result) > 40 {
		result = strings.TrimRight(result[:40], "-")
	}
	return result
}

func safeClientText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\x1b' {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	if strings.TrimSpace(value) == "" {
		return "editor host rejected the request"
	}
	return value
}
