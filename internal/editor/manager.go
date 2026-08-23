package editor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type HostStatus struct {
	Host     Host
	Snapshot HostSnapshot
	Error    string
	Busy     bool
}

type Manager struct {
	store        *StateStore
	executable   string
	state        ClientState
	clients      map[string]*HostClient
	starting     map[string]chan struct{}
	instanceLock *os.File
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	startWG      sync.WaitGroup
	closed       bool
	dirty        bool
	lastSave     time.Time
}

func NewManager(multicodexHome string) (*Manager, error) {
	store := NewStateStore(multicodexHome)
	instanceLock, err := store.AcquireInstanceLock()
	if err != nil {
		return nil, err
	}
	state, err := store.LoadOrCreate()
	if err != nil {
		releaseInstanceLock(instanceLock)
		return nil, err
	}
	if err := cleanupSSHControlPaths(multicodexHome, state.InstanceID); err != nil {
		releaseInstanceLock(instanceLock)
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		releaseInstanceLock(instanceLock)
		return nil, fmt.Errorf("resolve multicodex executable: %w", err)
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	return &Manager{store: store, executable: executable, state: state, clients: map[string]*HostClient{}, starting: map[string]chan struct{}{}, instanceLock: instanceLock, ctx: lifecycle, cancel: cancel}, nil
}

func (m *Manager) Context() context.Context { return m.ctx }

func (m *Manager) CheckLocal(ctx context.Context) error {
	host, ok := m.findHost(localHostID)
	if !ok {
		return errors.New("local editor host is not configured")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return errors.New("start local editor host")
	}
	var doctor DoctorResult
	if err := client.Call(ctx, "doctor", nil, &doctor); err != nil {
		return errors.New("local host readiness check failed")
	}
	if err := validateDoctorResult(doctor); err != nil {
		return err
	}
	if !doctor.OK {
		return fmt.Errorf("local host is not ready: %s", doctorIssueSummary(doctor))
	}
	return nil
}

func (m *Manager) State() ClientState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	state.Hosts = append([]Host(nil), m.state.Hosts...)
	for i := range state.Hosts {
		state.Hosts[i].Projects = append([]Project(nil), m.state.Hosts[i].Projects...)
	}
	state.Activities = append([]Activity(nil), m.state.Activities...)
	return state
}

func (m *Manager) Refresh(ctx context.Context) []HostStatus {
	m.mu.Lock()
	hosts := append([]Host(nil), m.state.Hosts...)
	m.mu.Unlock()

	statuses := refreshHosts(ctx, hosts, 10*time.Second, func(hostContext context.Context, host Host) HostStatus {
		status := HostStatus{Host: host}
		client, err := m.client(hostContext, host)
		if err == nil {
			err = client.Call(hostContext, "snapshot", nil, &status.Snapshot)
			if err == nil {
				err = validateHostSnapshot(host, status.Snapshot)
			}
		}
		if err != nil {
			status.Error = err.Error()
			status.Busy = errors.Is(err, errHostRequestCanceled)
			if errors.Is(err, errHostTransport) {
				m.dropClient(host.ID, client)
			}
		}
		return status
	})
	m.updateActivities(statuses)
	return statuses
}

func refreshHosts(ctx context.Context, hosts []Host, timeout time.Duration, refresh func(context.Context, Host) HostStatus) []HostStatus {
	statuses := make([]HostStatus, len(hosts))
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := min(8, len(hosts))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				hostContext, cancel := context.WithTimeout(ctx, timeout)
				statuses[index] = refresh(hostContext, hosts[index])
				cancel()
			}
		}()
	}
	for i := range hosts {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return statuses
}

func (m *Manager) AddHost(ctx context.Context, name, alias string) (Host, error) {
	if err := validateName(name, "host name"); err != nil {
		return Host{}, err
	}
	if err := validateSSHAlias(alias); err != nil {
		return Host{}, err
	}
	id, err := newID()
	if err != nil {
		return Host{}, err
	}
	host := Host{ID: id, Name: name, SSHAlias: alias}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Host{}, errors.New("editor is closing")
	}
	for _, existing := range m.state.Hosts {
		if existing.Name == name || existing.SSHAlias == alias {
			m.mu.Unlock()
			return Host{}, errors.New("host name and SSH alias must be unique")
		}
	}
	instanceID := m.state.InstanceID
	m.startWG.Add(1)
	m.mu.Unlock()
	defer m.startWG.Done()
	client, err := StartHostClient(ctx, m.executable, m.store.base, instanceID, host)
	if err != nil {
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, err
	}
	var doctor DoctorResult
	if err := client.Call(ctx, "doctor", nil, &doctor); err != nil {
		client.Close()
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, fmt.Errorf("SSH host %q readiness check failed", name)
	}
	if err := validateDoctorResult(doctor); err != nil {
		client.Close()
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, fmt.Errorf("SSH host %q returned unsafe readiness metadata", name)
	}
	if !doctor.OK {
		client.Close()
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, fmt.Errorf("SSH host %q is not ready: %s", name, doctorIssueSummary(doctor))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		client.Close()
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, errors.New("editor is closing")
	}
	for _, existing := range m.state.Hosts {
		if existing.Name == name || existing.SSHAlias == alias {
			client.Close()
			_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
			return Host{}, errors.New("host name and SSH alias must be unique")
		}
	}
	m.state.Hosts = append(m.state.Hosts, host)
	if err := m.store.Save(m.state); err != nil {
		m.state.Hosts = m.state.Hosts[:len(m.state.Hosts)-1]
		client.Close()
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		return Host{}, err
	}
	m.clients[host.ID] = client
	return host, nil
}

func validateDoctorResult(doctor DoctorResult) error {
	if len(doctor.Checks) > 8 || len(doctor.Issues) > 8 || doctor.OK && len(doctor.Issues) != 0 || !doctor.OK && len(doctor.Issues) == 0 {
		return errors.New("editor host returned unsafe readiness metadata")
	}
	for _, value := range append(append([]string(nil), doctor.Checks...), doctor.Issues...) {
		if safeClientText(value, 200) != value {
			return errors.New("editor host returned unsafe readiness metadata")
		}
	}
	return nil
}

func doctorIssueSummary(doctor DoctorResult) string {
	return safeClientText(strings.Join(doctor.Issues, "; "), 300)
}

func (m *Manager) AddProject(ctx context.Context, hostID, name, path string) (Project, error) {
	if err := validateName(name, "project name"); err != nil {
		return Project{}, err
	}
	if err := validateAbsolutePath(path, "project path"); err != nil {
		return Project{}, err
	}
	host, ok := m.findHost(hostID)
	if !ok {
		return Project{}, errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return Project{}, err
	}
	var info ProjectInfo
	if err := client.Call(ctx, "inspect_project", struct {
		Path string `json:"path"`
	}{path}, &info); err != nil {
		return Project{}, err
	}
	if info.Path != path || validateRemotePath(info.Path) != nil {
		return Project{}, errors.New("editor host returned unsafe project metadata")
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	project := Project{ID: id, Name: name, Path: info.Path}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Project{}, errors.New("editor is closing")
	}
	for i := range m.state.Hosts {
		if m.state.Hosts[i].ID != hostID {
			continue
		}
		for _, existing := range m.state.Hosts[i].Projects {
			if existing.Name == name || existing.Path == path {
				return Project{}, errors.New("project name and path must be unique on the host")
			}
		}
		m.state.Hosts[i].Projects = append(m.state.Hosts[i].Projects, project)
		if err := m.store.Save(m.state); err != nil {
			m.state.Hosts[i].Projects = m.state.Hosts[i].Projects[:len(m.state.Hosts[i].Projects)-1]
			return Project{}, err
		}
		return project, nil
	}
	return Project{}, errors.New("host no longer exists")
}

func (m *Manager) CreateWorkspace(ctx context.Context, hostID string, request CreateWorkspaceRequest) (Workspace, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return Workspace{}, errors.New("host no longer exists")
	}
	project, ok := findProject(host, request.ProjectID)
	if !ok || project.Path != request.ProjectPath {
		return Workspace{}, errors.New("project no longer exists or its path changed")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return Workspace{}, err
	}
	var workspace Workspace
	if err := client.Call(ctx, "create_workspace", request, &workspace); err != nil {
		return Workspace{}, err
	}
	if err := validateCreatedWorkspace(request, workspace); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (m *Manager) CreateWindow(ctx context.Context, hostID string, request CreateWindowRequest) (Window, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return Window{}, errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return Window{}, err
	}
	var window Window
	if err := client.Call(ctx, "create_window", request, &window); err != nil {
		return Window{}, err
	}
	if err := validateCreatedWindow(request, window); err != nil {
		return Window{}, err
	}
	return window, nil
}

func (m *Manager) OpenProjectWindow(ctx context.Context, hostID string, request OpenProjectWindowRequest) (OpenProjectWindowResult, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return OpenProjectWindowResult{}, errors.New("host no longer exists")
	}
	project, ok := findProject(host, request.ProjectID)
	if !ok || project.Path != request.ProjectPath {
		return OpenProjectWindowResult{}, errors.New("project no longer exists or its path changed")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return OpenProjectWindowResult{}, err
	}
	var result OpenProjectWindowResult
	if err := client.Call(ctx, "open_project_window", request, &result); err != nil {
		return OpenProjectWindowResult{}, err
	}
	if err := validateOpenedProjectWindow(request, result.Window); err != nil {
		return OpenProjectWindowResult{}, err
	}
	return result, nil
}

func (m *Manager) ListTmuxSessions(ctx context.Context, hostID string, request ListTmuxSessionsRequest) ([]TmuxSessionCandidate, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return nil, errors.New("host no longer exists")
	}
	project, ok := findProject(host, request.ProjectID)
	if !ok || project.Path != request.ProjectPath {
		return nil, errors.New("project no longer exists or its path changed")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return nil, err
	}
	var candidates []TmuxSessionCandidate
	if err := client.Call(ctx, "list_tmux_sessions", request, &candidates); err != nil {
		return nil, err
	}
	if len(candidates) > 100 {
		return nil, errors.New("editor host returned too many tmux sessions")
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if validateTmuxSessionName(candidate.Name) != nil || validateName(candidate.Command, "tmux command") != nil || seen[candidate.Name] {
			return nil, errors.New("editor host returned unsafe tmux session metadata")
		}
		seen[candidate.Name] = true
	}
	return candidates, nil
}

func (m *Manager) AdoptTmuxSession(ctx context.Context, hostID string, request AdoptTmuxSessionRequest) (AdoptedTmuxSession, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return AdoptedTmuxSession{}, errors.New("host no longer exists")
	}
	project, ok := findProject(host, request.ProjectID)
	if !ok || project.Path != request.ProjectPath {
		return AdoptedTmuxSession{}, errors.New("project no longer exists or its path changed")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return AdoptedTmuxSession{}, err
	}
	var adopted AdoptedTmuxSession
	if err := client.Call(ctx, "adopt_tmux_session", request, &adopted); err != nil {
		return AdoptedTmuxSession{}, err
	}
	if err := validateAdoptedTmuxSession(request, adopted); err != nil {
		return AdoptedTmuxSession{}, err
	}
	return adopted, nil
}

func (m *Manager) CreateWorkspaceWithWindow(ctx context.Context, hostID string, request CreateWorkspaceRequest) (Workspace, Window, error) {
	workspace, err := m.CreateWorkspace(ctx, hostID, request)
	if err != nil {
		return Workspace{}, Window{}, err
	}
	window, err := m.CreateWindow(ctx, hostID, CreateWindowRequest{WorkspaceID: workspace.ID})
	if err == nil {
		return workspace, window, nil
	}
	rollbackContext, cancel := context.WithTimeout(m.Context(), 30*time.Second)
	defer cancel()
	result, rollbackErr := m.DeleteWorkspace(rollbackContext, hostID, DeleteRequest{ID: workspace.ID})
	if rollbackErr == nil && result.Deleted {
		return Workspace{}, Window{}, fmt.Errorf("create first terminal: %w; workspace creation was rolled back", err)
	}
	return workspace, Window{}, fmt.Errorf("workspace was created, but its first terminal failed: %v; select the workspace and press Enter to retry", err)
}

func (m *Manager) RenameWorkspace(ctx context.Context, hostID string, request RenameRequest) error {
	if err := validateID(request.ID, "workspace identifier"); err != nil {
		return err
	}
	if err := validateName(request.Name, "workspace name"); err != nil {
		return err
	}
	host, ok := m.findHost(hostID)
	if !ok {
		return errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return err
	}
	return client.Call(ctx, "rename_workspace", request, nil)
}

func (m *Manager) RenameWindow(ctx context.Context, hostID string, request RenameRequest) error {
	if err := validateID(request.ID, "window identifier"); err != nil {
		return err
	}
	if err := validateName(request.Name, "window name"); err != nil {
		return err
	}
	host, ok := m.findHost(hostID)
	if !ok {
		return errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return err
	}
	return client.Call(ctx, "rename_window", request, nil)
}

func (m *Manager) PutAttachment(ctx context.Context, hostID string, request PutAttachmentRequest) (AttachmentFile, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return AttachmentFile{}, errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return AttachmentFile{}, err
	}
	var attachment AttachmentFile
	if err := client.Call(ctx, "put_attachment", request, &attachment); err != nil {
		return AttachmentFile{}, err
	}
	if err := validateAttachmentResult(request, attachment); err != nil {
		return AttachmentFile{}, err
	}
	return attachment, nil
}

func (m *Manager) TouchWindow(ctx context.Context, hostID, windowID string) error {
	host, ok := m.findHost(hostID)
	if !ok {
		return errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return err
	}
	return client.Call(ctx, "touch_window", struct {
		ID string `json:"id"`
	}{windowID}, nil)
}

func (m *Manager) CopyMode(ctx context.Context, hostID, windowID string) error {
	host, ok := m.findHost(hostID)
	if !ok {
		return errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return err
	}
	return client.Call(ctx, "copy_mode", struct {
		ID string `json:"id"`
	}{windowID}, nil)
}

func (m *Manager) AttachWindow(ctx context.Context, hostID string, window Window, width, height int) (*Attachment, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return nil, errors.New("host no longer exists")
	}
	if err := m.TouchWindow(ctx, hostID, window.ID); err != nil {
		if errors.Is(err, errHostRequestCanceled) {
			return nil, errors.New("host is busy; retry the window")
		}
		return nil, errors.New("refuse to attach without exact tmux ownership")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("editor is closing")
	}
	instanceID := m.state.InstanceID
	m.mu.Unlock()
	return attachWindowPTY(m.ctx, host, m.SSHControlPath(host), instanceID, window, width, height)
}

func (m *Manager) DeleteWindow(ctx context.Context, hostID string, request DeleteRequest) (DeleteResult, error) {
	return m.delete(ctx, hostID, "delete_window", request)
}

func (m *Manager) DeleteWorkspace(ctx context.Context, hostID string, request DeleteRequest) (DeleteResult, error) {
	return m.delete(ctx, hostID, "delete_workspace", request)
}

func (m *Manager) delete(ctx context.Context, hostID, method string, request DeleteRequest) (DeleteResult, error) {
	host, ok := m.findHost(hostID)
	if !ok {
		return DeleteResult{}, errors.New("host no longer exists")
	}
	client, err := m.client(ctx, host)
	if err != nil {
		return DeleteResult{}, err
	}
	var result DeleteResult
	if err := client.Call(ctx, method, request, &result); err != nil {
		return DeleteResult{}, err
	}
	result.Reason = safeClientText(result.Reason, 300)
	if result.Deleted {
		result.Reason = ""
		if method == "delete_window" {
			if err := m.clearSelectedWindow(request.ID); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (m *Manager) clearSelectedWindow(windowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.SelectedWindowID != windowID {
		return nil
	}
	m.state.SelectedWindowID = ""
	m.dirty = true
	return m.maybeSaveLocked(true)
}

func validateHostSnapshot(host Host, snapshot HostSnapshot) error {
	if snapshot.Protocol != hostProtocol {
		return errors.New("editor host returned an incompatible snapshot")
	}
	projectPaths := make(map[string]string, len(host.Projects))
	for _, project := range host.Projects {
		projectPaths[project.ID] = project.Path
	}
	workspaceIDs := make(map[string]bool, len(snapshot.Workspaces))
	externalWorkspaces := make(map[string]bool, len(snapshot.Workspaces))
	for _, workspace := range snapshot.Workspaces {
		if validateID(workspace.ID, "workspace identifier") != nil || projectPaths[workspace.ProjectID] == "" || workspace.ProjectPath != projectPaths[workspace.ProjectID] || workspaceIDs[workspace.ID] || validateName(workspace.Name, "workspace name") != nil || validateRemotePath(workspace.Path) != nil || validateRemotePath(workspace.ProjectPath) != nil {
			return errors.New("editor host returned unsafe workspace metadata")
		}
		if workspace.CreatePending {
			return errors.New("editor host returned unsafe workspace metadata")
		}
		if workspace.Git {
			if validateRemotePath(workspace.GitCommonDir) != nil {
				return errors.New("editor host returned unsafe Git workspace metadata")
			}
			if workspace.External {
				if workspace.Path != workspace.ProjectPath || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
					return errors.New("editor host returned unsafe preserved Git workspace metadata")
				}
			} else if !validOwnedBranch(workspace.Branch, workspace.ID) || !safeStoredBaseRef(workspace.BaseRef) || !workspace.WorktreeLocked && !workspace.Unavailable {
				return errors.New("editor host returned unsafe Git workspace metadata")
			}
		} else if workspace.Path != workspace.ProjectPath || workspace.GitCommonDir != "" || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
			return errors.New("editor host returned unsafe non-Git workspace metadata")
		}
		workspaceIDs[workspace.ID] = true
		externalWorkspaces[workspace.ID] = workspace.External
	}
	windowIDs := make(map[string]bool, len(snapshot.Windows))
	projectWindows := make(map[string]bool)
	adoptedSessions := make(map[string]bool)
	for _, window := range snapshot.Windows {
		workspaceWindow := window.WorkspaceID != "" && window.ProjectID == "" && window.ProjectPath == "" && workspaceIDs[window.WorkspaceID]
		projectWindow := window.WorkspaceID == "" && projectPaths[window.ProjectID] != "" && window.ProjectPath == projectPaths[window.ProjectID] && !projectWindows[window.ProjectID] && window.Name == projectWindowName && !window.Adopted
		if validateID(window.ID, "window identifier") != nil || windowIDs[window.ID] || !workspaceWindow && !projectWindow || validateName(window.Name, "window name") != nil || window.CreatePending || window.Alive && window.Exited || window.PaneHash != "" && !paneHashPattern.MatchString(window.PaneHash) {
			return errors.New("editor host returned unsafe window metadata")
		}
		if projectWindow {
			projectWindows[window.ProjectID] = true
		}
		if window.Adopted {
			if !externalWorkspaces[window.WorkspaceID] || validateTmuxSessionName(window.Session) != nil || !tmuxIDPattern.MatchString(window.TmuxSessionID) || adoptedSessions[window.Session] || adoptedSessions[window.TmuxSessionID] {
				return errors.New("editor host returned unsafe adopted tmux metadata")
			}
			adoptedSessions[window.Session] = true
			adoptedSessions[window.TmuxSessionID] = true
		} else if window.Session != "mce-"+window.ID || window.TmuxSessionID != "" {
			return errors.New("editor host returned unsafe window metadata")
		}
		windowIDs[window.ID] = true
	}
	return nil
}

func validateCreatedWorkspace(request CreateWorkspaceRequest, workspace Workspace) error {
	if validateID(workspace.ID, "workspace identifier") != nil || workspace.ProjectID != request.ProjectID || workspace.ProjectPath != request.ProjectPath || workspace.Name != request.Name || validateRemotePath(workspace.Path) != nil || workspace.CreatePending || workspace.DeletePending || workspace.Unavailable {
		return errors.New("editor host returned unsafe workspace metadata")
	}
	if workspace.External {
		return errors.New("editor host returned an unexpected preserved workspace")
	}
	if workspace.Git {
		if validateRemotePath(workspace.GitCommonDir) != nil || workspace.Branch != "multicodex/"+slug(workspace.Name)+"-"+workspace.ID[:8] || !safeStoredBaseRef(workspace.BaseRef) || !workspace.WorktreeLocked {
			return errors.New("editor host returned unsafe Git workspace metadata")
		}
	} else if workspace.Path != workspace.ProjectPath || workspace.GitCommonDir != "" || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
		return errors.New("editor host returned unsafe non-Git workspace metadata")
	}
	return nil
}

func validateCreatedWindow(request CreateWindowRequest, window Window) error {
	if validateID(window.ID, "window identifier") != nil || window.WorkspaceID != request.WorkspaceID || window.ProjectID != "" || window.ProjectPath != "" || validateName(window.Name, "window name") != nil || window.Session != "mce-"+window.ID || window.TmuxSessionID != "" || window.Adopted || window.CreatePending || window.DeletePending {
		return errors.New("editor host returned unsafe window metadata")
	}
	return nil
}

func validateOpenedProjectWindow(request OpenProjectWindowRequest, window Window) error {
	if validateID(window.ID, "window identifier") != nil || window.WorkspaceID != "" || window.ProjectID != request.ProjectID || window.ProjectPath != request.ProjectPath || window.Name != projectWindowName || window.Session != "mce-"+window.ID || window.TmuxSessionID != "" || window.Adopted || window.CreatePending || window.DeletePending {
		return errors.New("editor host returned unsafe project terminal metadata")
	}
	return nil
}

func validateAdoptedTmuxSession(request AdoptTmuxSessionRequest, adopted AdoptedTmuxSession) error {
	workspace, window := adopted.Workspace, adopted.Window
	if validateID(workspace.ID, "workspace identifier") != nil || workspace.ProjectID != request.ProjectID || workspace.ProjectPath != request.ProjectPath || workspace.Path != request.ProjectPath || workspace.Name != request.WorkspaceName || workspace.CreatePending || workspace.DeletePending || workspace.Unavailable {
		return errors.New("editor host returned unsafe adopted workspace metadata")
	}
	if workspace.Git {
		if !workspace.External || validateRemotePath(workspace.GitCommonDir) != nil || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
			return errors.New("editor host returned unsafe adopted Git workspace metadata")
		}
	} else if !workspace.External || workspace.GitCommonDir != "" || workspace.Branch != "" || workspace.BaseRef != "" || workspace.WorktreeLocked {
		return errors.New("editor host returned unsafe adopted workspace metadata")
	}
	if validateID(window.ID, "window identifier") != nil || window.WorkspaceID != workspace.ID || validateName(window.Name, "window name") != nil || !window.Adopted || window.Session != request.Session || !tmuxIDPattern.MatchString(window.TmuxSessionID) || window.CreatePending || window.DeletePending {
		return errors.New("editor host returned unsafe adopted tmux metadata")
	}
	return nil
}

func validateAttachmentResult(request PutAttachmentRequest, attachment AttachmentFile) error {
	if validateID(attachment.ID, "attachment identifier") != nil || attachment.WorkspaceID != request.WorkspaceID || attachment.ProjectID != request.ProjectID || (request.WorkspaceID == "") == (request.ProjectID == "") || validateRemotePath(attachment.Path) != nil || attachment.CreatePending {
		return errors.New("editor host returned unsafe attachment metadata")
	}
	return nil
}

func (m *Manager) CleanupAll(ctx context.Context) map[string]CleanupResult {
	m.mu.Lock()
	hosts := append([]Host(nil), m.state.Hosts...)
	m.mu.Unlock()
	values := make([]CleanupResult, len(hosts))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range min(8, len(hosts)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				hostContext, cancel := context.WithTimeout(ctx, 35*time.Second)
				values[index] = m.cleanupHost(hostContext, hosts[index])
				cancel()
			}
		}()
	}
	for index := range hosts {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	results := make(map[string]CleanupResult, len(hosts))
	for index, host := range hosts {
		results[host.ID] = values[index]
	}
	return results
}

func (m *Manager) cleanupHost(ctx context.Context, host Host) CleanupResult {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return CleanupResult{Skipped: []string{"host cleanup unavailable"}}
	}
	instanceID := m.state.InstanceID
	executable := m.executable
	home := m.store.base
	m.startWG.Add(1)
	m.mu.Unlock()
	defer m.startWG.Done()

	client, err := startIsolatedHostClient(ctx, executable, home, instanceID, host)
	if err != nil {
		return CleanupResult{Skipped: []string{"host cleanup unavailable"}}
	}
	defer client.Close()
	var result CleanupResult
	if err := client.Call(ctx, "cleanup", nil, &result); err != nil {
		return CleanupResult{Skipped: []string{"host cleanup unavailable"}}
	}
	if err := validateCleanupResult(result); err != nil {
		return CleanupResult{Skipped: []string{"host cleanup response was invalid"}}
	}
	return result
}

func validateCleanupResult(result CleanupResult) error {
	if result.WindowsDeleted < 0 || result.WindowsDeleted > 1_000_000 || result.WorkspacesDeleted < 0 || result.WorkspacesDeleted > 1_000_000 || result.AttachmentsDeleted < 0 || result.AttachmentsDeleted > 1_000_000 || len(result.Skipped) > 1000 {
		return errors.New("invalid cleanup result")
	}
	for _, note := range result.Skipped {
		if note == "" || len(note) > 512 || safeClientText(note, 512) != note {
			return errors.New("invalid cleanup result")
		}
	}
	return nil
}

func (m *Manager) SetSelectedWindow(windowID string) error {
	if windowID != "" {
		if err := validateID(windowID, "window identifier"); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("editor is closing")
	}
	if m.state.SelectedWindowID == windowID {
		return nil
	}
	m.state.SelectedWindowID = windowID
	m.dirty = true
	return m.maybeSaveLocked(true)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	instanceID := m.state.InstanceID
	clients := make([]*HostClient, 0, len(m.clients))
	for id, client := range m.clients {
		clients = append(clients, client)
		delete(m.clients, id)
	}
	err := m.maybeSaveLocked(true)
	m.mu.Unlock()
	m.startWG.Wait()
	for _, client := range clients {
		_ = client.Close()
	}
	if cleanupErr := cleanupSSHControlPaths(m.store.base, instanceID); err == nil {
		err = cleanupErr
	}
	releaseInstanceLock(m.instanceLock)
	m.instanceLock = nil
	return err
}

func (m *Manager) client(ctx context.Context, host Host) (*HostClient, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("editor is closing")
		}
		if existing := m.clients[host.ID]; existing != nil {
			m.mu.Unlock()
			return existing, nil
		}
		if pending := m.starting[host.ID]; pending != nil {
			m.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, errors.New("editor host connection timed out")
			}
		}
		if m.starting == nil {
			m.starting = map[string]chan struct{}{}
		}
		m.starting[host.ID] = make(chan struct{})
		m.startWG.Add(1)
		break
	}
	instanceID := m.state.InstanceID
	m.mu.Unlock()
	defer m.startWG.Done()

	client, err := StartHostClient(ctx, m.executable, m.store.base, instanceID, host)
	if err != nil && host.ID != localHostID {
		_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
	}
	m.mu.Lock()
	pending := m.starting[host.ID]
	delete(m.starting, host.ID)
	if pending != nil {
		close(pending)
	}
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.closed {
		m.mu.Unlock()
		client.Close()
		if host.ID != localHostID {
			_ = cleanupSSHControlPath(m.store.base, instanceID, host.ID)
		}
		return nil, errors.New("editor is closing")
	}
	m.clients[host.ID] = client
	m.mu.Unlock()
	return client, nil
}

func (m *Manager) SSHControlPath(host Host) string {
	if host.ID == localHostID {
		return ""
	}
	path, _ := prepareSSHControlPath(m.store.base, m.state.InstanceID, host.ID)
	return path
}

func (m *Manager) dropClient(hostID string, client *HostClient) {
	if client == nil {
		return
	}
	m.mu.Lock()
	removed := false
	if m.clients[hostID] == client {
		delete(m.clients, hostID)
		removed = true
	}
	m.mu.Unlock()
	client.Close()
	if removed && hostID != localHostID {
		_ = cleanupSSHControlPath(m.store.base, m.state.InstanceID, hostID)
	}
}

func (m *Manager) findHost(id string) (Host, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, host := range m.state.Hosts {
		if host.ID == id {
			return host, true
		}
	}
	return Host{}, false
}

func findProject(host Host, id string) (Project, bool) {
	for _, project := range host.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return Project{}, false
}

func (m *Manager) updateActivities(statuses []HostStatus) {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	activityIndex := map[string]int{}
	for i, activity := range m.state.Activities {
		activityIndex[activity.HostID+"/"+activity.WindowID] = i
	}
	seen := map[string]bool{}
	reachable := map[string]bool{}
	for _, status := range statuses {
		if status.Error != "" {
			continue
		}
		reachable[status.Host.ID] = true
		for _, window := range status.Snapshot.Windows {
			key := status.Host.ID + "/" + window.ID
			seen[key] = true
			if i, ok := activityIndex[key]; ok {
				if window.PaneHash != "" && window.PaneHash != m.state.Activities[i].PaneHash {
					m.state.Activities[i].PaneHash = window.PaneHash
					m.state.Activities[i].ChangedAt = now
					m.dirty = true
				}
			} else {
				m.state.Activities = append(m.state.Activities, Activity{HostID: status.Host.ID, WindowID: window.ID, PaneHash: window.PaneHash, ChangedAt: now})
				m.dirty = true
			}
		}
	}
	kept := m.state.Activities[:0]
	for _, activity := range m.state.Activities {
		if !reachable[activity.HostID] || seen[activity.HostID+"/"+activity.WindowID] {
			kept = append(kept, activity)
		} else {
			m.dirty = true
		}
	}
	m.state.Activities = kept
	_ = m.maybeSaveLocked(false)
}

func (m *Manager) maybeSaveLocked(force bool) error {
	if !m.dirty {
		return nil
	}
	if !force && time.Since(m.lastSave) < 30*time.Second {
		return nil
	}
	if err := m.store.Save(m.state); err != nil {
		return err
	}
	m.dirty = false
	m.lastSave = time.Now()
	return nil
}

func sortedProjectsByActivity(state ClientState, statuses []HostStatus, now time.Time) []ProjectLocation {
	type score struct {
		location ProjectLocation
		activity time.Time
		rank     int
	}
	activities := map[string]time.Time{}
	for _, activity := range state.Activities {
		activities[activity.HostID+"/"+activity.WindowID] = activity.ChangedAt
	}
	var scored []score
	for _, status := range statuses {
		workspaceByProject := map[string][]Workspace{}
		for _, workspace := range status.Snapshot.Workspaces {
			workspaceByProject[workspace.ProjectID] = append(workspaceByProject[workspace.ProjectID], workspace)
		}
		for _, project := range status.Host.Projects {
			workspaces := workspaceByProject[project.ID]
			location := ProjectLocation{Host: status.Host, Project: project, HostError: status.Error}
			var terminalActivity time.Time
			var workspaceActivity time.Time
			workspaceIDs := map[string]bool{}
			windowsByWorkspace := map[string][]Window{}
			for _, workspace := range workspaces {
				workspaceIDs[workspace.ID] = true
			}
			for _, window := range status.Snapshot.Windows {
				if window.ProjectID == project.ID {
					location.ProjectWindow = window
					terminalActivity = windowActivity(status.Host.ID, window, activities)
					continue
				}
				if workspaceIDs[window.WorkspaceID] {
					windowsByWorkspace[window.WorkspaceID] = append(windowsByWorkspace[window.WorkspaceID], window)
				}
			}
			type workspaceScore struct {
				workspace Workspace
				windows   []Window
				activity  time.Time
				populated bool
			}
			workspaceScores := make([]workspaceScore, 0, len(workspaces))
			for _, workspace := range workspaces {
				windows := windowsByWorkspace[workspace.ID]
				sort.SliceStable(windows, func(i, j int) bool {
					left := windowActivity(status.Host.ID, windows[i], activities)
					right := windowActivity(status.Host.ID, windows[j], activities)
					if order := compareActivity(left, right, now); order != 0 {
						return order < 0
					}
					if windows[i].Name != windows[j].Name {
						return windows[i].Name < windows[j].Name
					}
					return windows[i].ID < windows[j].ID
				})
				activity := workspace.LastUsedAt
				populated := len(windows) > 0
				if populated {
					activity = windowActivity(status.Host.ID, windows[0], activities)
				}
				workspaceScores = append(workspaceScores, workspaceScore{workspace: workspace, windows: windows, activity: activity, populated: populated})
			}
			sort.SliceStable(workspaceScores, func(i, j int) bool {
				if workspaceScores[i].populated != workspaceScores[j].populated {
					return workspaceScores[i].populated
				}
				if workspaceScores[i].populated {
					if order := compareActivity(workspaceScores[i].activity, workspaceScores[j].activity, now); order != 0 {
						return order < 0
					}
				} else if !workspaceScores[i].activity.Equal(workspaceScores[j].activity) {
					return workspaceScores[i].activity.After(workspaceScores[j].activity)
				}
				if workspaceScores[i].workspace.Name != workspaceScores[j].workspace.Name {
					return workspaceScores[i].workspace.Name < workspaceScores[j].workspace.Name
				}
				return workspaceScores[i].workspace.ID < workspaceScores[j].workspace.ID
			})
			for _, workspace := range workspaceScores {
				location.Workspaces = append(location.Workspaces, workspace.workspace)
				location.Windows = append(location.Windows, workspace.windows...)
				if workspace.populated && workspace.activity.After(terminalActivity) {
					terminalActivity = workspace.activity
				}
				if workspace.activity.After(workspaceActivity) {
					workspaceActivity = workspace.activity
				}
			}
			rank := 2
			activity := time.Time{}
			switch {
			case location.ProjectWindow.ID != "" || len(location.Windows) > 0:
				rank, activity = 0, terminalActivity
			case len(location.Workspaces) > 0:
				rank, activity = 1, workspaceActivity
			}
			location.LastActivity = activity
			scored = append(scored, score{location: location, activity: activity, rank: rank})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].rank != scored[j].rank {
			return scored[i].rank < scored[j].rank
		}
		if scored[i].rank == 0 {
			if order := compareActivity(scored[i].activity, scored[j].activity, now); order != 0 {
				return order < 0
			}
		} else if !scored[i].activity.Equal(scored[j].activity) {
			return scored[i].activity.After(scored[j].activity)
		}
		if scored[i].location.Project.Name != scored[j].location.Project.Name {
			return scored[i].location.Project.Name < scored[j].location.Project.Name
		}
		if scored[i].location.Host.Name != scored[j].location.Host.Name {
			return scored[i].location.Host.Name < scored[j].location.Host.Name
		}
		return scored[i].location.Project.ID < scored[j].location.Project.ID
	})
	result := make([]ProjectLocation, len(scored))
	for i := range scored {
		result[i] = scored[i].location
	}
	return result
}

func isRecentActivity(activity, now time.Time) bool {
	age := now.Sub(activity)
	return !activity.IsZero() && age >= 0 && age <= recentOutputWindow
}

func compareActivity(left, right, now time.Time) int {
	leftRecent := isRecentActivity(left, now)
	rightRecent := isRecentActivity(right, now)
	switch {
	case leftRecent && !rightRecent:
		return -1
	case rightRecent && !leftRecent:
		return 1
	case leftRecent:
		return 0
	case left.After(right):
		return -1
	case right.After(left):
		return 1
	default:
		return 0
	}
}

func windowActivity(hostID string, window Window, activities map[string]time.Time) time.Time {
	if changed := activities[hostID+"/"+window.ID]; !changed.IsZero() {
		return changed
	}
	if !window.LastUsedAt.IsZero() {
		return window.LastUsedAt
	}
	return window.CreatedAt
}

type ProjectLocation struct {
	Host          Host
	Project       Project
	ProjectWindow Window
	Workspaces    []Workspace
	Windows       []Window
	LastActivity  time.Time
	HostError     string
}
