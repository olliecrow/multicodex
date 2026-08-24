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
	stateVersion       = 1
	hostProtocol       = 5
	historyLimit       = 60000
	activityRows       = 200
	activityQuietAfter = 30 * time.Second
	cleanupAfter       = 7 * 24 * time.Hour
	maxAttachment      = 16 << 20
	maxClientState     = 8 << 20
	maxHostState       = 16 << 20
	maxStateRecords    = 100_000
	minimumWidth       = 80
	minimumHeight      = 24
	localHostID        = "local"
	localHostName      = "Local"
	defaultWindowName  = "Terminal"
	projectWindowName  = "Project terminal"
)

var (
	idPattern       = regexp.MustCompile(`^[a-f0-9]{24}$`)
	gitOIDPattern   = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
	tmuxIDPattern   = regexp.MustCompile(`^\$[0-9]+$`)
	tmuxNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	sshAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type ClientState struct {
	Version          int    `json:"version"`
	InstanceID       string `json:"instance_id"`
	Hosts            []Host `json:"hosts"`
	SelectedWindowID string `json:"selected_window_id,omitempty"`
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

type HostSnapshot struct {
	Protocol   int         `json:"protocol"`
	Workspaces []Workspace `json:"workspaces"`
	Windows    []Window    `json:"windows"`
}

type Workspace struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	ProjectPath    string    `json:"project_path"`
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	Git            bool      `json:"git"`
	External       bool      `json:"external,omitempty"`
	GitCommonDir   string    `json:"git_common_dir,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	BaseRef        string    `json:"base_ref,omitempty"`
	WorktreeLocked bool      `json:"worktree_locked,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsedAt     time.Time `json:"last_used_at"`
	CreatePending  bool      `json:"create_pending,omitempty"`
	DeletePending  bool      `json:"delete_pending,omitempty"`
	Unavailable    bool      `json:"unavailable,omitempty"`
}

type Window struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id,omitempty"`
	ProjectID     string    `json:"project_id,omitempty"`
	ProjectPath   string    `json:"project_path,omitempty"`
	Name          string    `json:"name"`
	Session       string    `json:"session"`
	TmuxSessionID string    `json:"tmux_session_id,omitempty"`
	Adopted       bool      `json:"adopted,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
	Alive         bool      `json:"alive"`
	Exited        bool      `json:"exited,omitempty"`
	CreatePending bool      `json:"create_pending,omitempty"`
	DeletePending bool      `json:"delete_pending,omitempty"`
	RecentOutput  bool      `json:"recent_output,omitempty"`
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
}

type OpenProjectWindowRequest struct {
	ProjectID   string `json:"project_id"`
	ProjectPath string `json:"project_path"`
}

type OpenProjectWindowResult struct {
	Window  Window `json:"window"`
	Created bool   `json:"created"`
}

type RenameRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
	WorkspaceID   string    `json:"workspace_id,omitempty"`
	ProjectID     string    `json:"project_id,omitempty"`
	Path          string    `json:"path"`
	CreatedAt     time.Time `json:"created_at"`
	CreatePending bool      `json:"create_pending,omitempty"`
}

type PutAttachmentRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Extension   string `json:"extension,omitempty"`
	Data        []byte `json:"data"`
	Image       bool   `json:"image,omitempty"`
}

func windowTargetID(window Window) string {
	if window.WorkspaceID != "" {
		return window.WorkspaceID
	}
	return window.ProjectID
}

func attachmentTargetID(attachment AttachmentFile) string {
	if attachment.WorkspaceID != "" {
		return attachment.WorkspaceID
	}
	return attachment.ProjectID
}

func attachmentRequestTargetID(request PutAttachmentRequest) string {
	if request.WorkspaceID != "" {
		return request.WorkspaceID
	}
	return request.ProjectID
}

type ProjectInfo struct {
	Path string `json:"path"`
	Git  bool   `json:"git"`
}

type TmuxSessionCandidate struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	SessionID string `json:"-"`
	PanePID   string `json:"-"`
}

type ListTmuxSessionsRequest struct {
	ProjectID   string `json:"project_id"`
	ProjectPath string `json:"project_path"`
}

type AdoptTmuxSessionRequest struct {
	ProjectID     string `json:"project_id"`
	ProjectPath   string `json:"project_path"`
	WorkspaceName string `json:"workspace_name"`
	Session       string `json:"session"`
}

type AdoptedTmuxSession struct {
	Workspace Workspace `json:"workspace"`
	Window    Window    `json:"window"`
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
		if unsafeDisplayRune(r) {
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

func validateTmuxSessionName(value string) error {
	if err := validateName(value, "tmux session name"); err != nil {
		return err
	}
	if !tmuxNamePattern.MatchString(value) {
		return errors.New("tmux session name must contain only letters, numbers, underscore, or hyphen")
	}
	return nil
}

func validateAbsolutePath(value, field string) error {
	if value == "" || len(value) > 4096 || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("%s must be a clean absolute path", field)
	}
	for _, r := range value {
		if unsafeDisplayRune(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func validateRemotePath(value string) error {
	return validateAbsolutePath(value, "remote path")
}

func nextDefaultName(base string, existing map[string]bool) string {
	if !existing[base] {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s %d", base, index)
		if !existing[candidate] {
			return candidate
		}
	}
}

func validOwnedBranch(value, workspaceID string) bool {
	if validateID(workspaceID, "workspace identifier") != nil || !strings.HasPrefix(value, "multicodex/") {
		return false
	}
	remainder := strings.TrimPrefix(value, "multicodex/")
	suffix := "-" + workspaceID[:8]
	if !strings.HasSuffix(remainder, suffix) || !safeGitRefPart(remainder) {
		return false
	}
	name := strings.TrimSuffix(remainder, suffix)
	return name != "" && slug(name) == name
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
		if unsafeDisplayRune(r) {
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

func unsafeDisplayRune(r rune) bool {
	return r == '\x1b' || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}
