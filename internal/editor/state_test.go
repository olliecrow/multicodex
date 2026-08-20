package editor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateStoreRoundTripCreatesPrivateState(t *testing.T) {
	store := NewStateStore(filepath.Join(t.TempDir(), "multicodex"))
	state, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if state.InstanceID == "" || len(state.Hosts) != 1 || state.Hosts[0].ID != localHostID {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	projectID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	state.Hosts[0].Projects = []Project{{ID: projectID, Name: "Demo", Path: "/tmp/demo"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Hosts[0].Projects[0].Name != "Demo" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{store.root, store.path} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0o700)
			if path == store.path {
				want = 0o600
			}
			if info.Mode().Perm() != want {
				t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
			}
		}
	}
}

func TestHostStoreRejectsIntermediateEditorSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	base := filepath.Join(t.TempDir(), "multicodex")
	target := filepath.Join(t.TempDir(), "target")
	for _, path := range []string{base, target} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, filepath.Join(base, "editor")); err != nil {
		t.Fatal(err)
	}
	service, err := NewHostService(base, testInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(context.Background()); err == nil {
		t.Fatal("expected intermediate editor symlink rejection")
	}
	if doctor := service.Doctor(context.Background()); doctor.OK {
		t.Fatal("doctor accepted an intermediate editor symlink")
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("host state escaped through editor symlink: entries=%v err=%v", entries, err)
	}
}

func TestStateStoreRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "editor")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	store := &StateStore{base: root, root: link, path: filepath.Join(link, "state.json")}
	if _, err := store.LoadOrCreate(); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestValidateClientStateRejectsUnsafeSSHAndPaths(t *testing.T) {
	state, err := NewClientState()
	if err != nil {
		t.Fatal(err)
	}
	hostID, _ := newID()
	projectID, _ := newID()
	state.Hosts = append(state.Hosts, Host{
		ID: hostID, Name: "Remote", SSHAlias: "-oProxyCommand=bad",
		Projects: []Project{{ID: projectID, Name: "Project", Path: "relative"}},
	})
	if err := validateClientState(state); err == nil {
		t.Fatal("expected unsafe SSH alias rejection")
	}
}

func TestSlugIsPortableAndBounded(t *testing.T) {
	got := slug(" Fix payment/Parser 🧪 ")
	if got != "fix-payment-parser" {
		t.Fatalf("slug = %q", got)
	}
	if got := slug("🧪"); got != "workspace" {
		t.Fatalf("fallback slug = %q", got)
	}
}

func TestAutomaticWindowNamesAndRenamedWorkspaceBranchIdentity(t *testing.T) {
	existing := map[string]bool{"Terminal": true, "Terminal 2": true}
	if got := nextDefaultName("Terminal", existing); got != "Terminal 3" {
		t.Fatalf("next automatic window name = %q", got)
	}
	workspaceID := "0123456789abcdef01234567"
	if !validOwnedBranch("multicodex/initial-name-"+workspaceID[:8], workspaceID) {
		t.Fatal("valid owned branch was rejected after a display rename")
	}
	for _, value := range []string{
		"multicodex/initial-name-deadbeef",
		"other/initial-name-" + workspaceID[:8],
		"multicodex/../initial-name-" + workspaceID[:8],
	} {
		if validOwnedBranch(value, workspaceID) {
			t.Fatalf("unsafe branch identity accepted: %q", value)
		}
	}
}

func TestStateStoreAllowsOnlyOneActiveEditorClient(t *testing.T) {
	home := filepath.Join(t.TempDir(), "multicodex")
	store := NewStateStore(home)
	first, err := store.AcquireInstanceLock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseInstanceLock(first)
	if second, err := store.AcquireInstanceLock(); err == nil {
		releaseInstanceLock(second)
		t.Fatal("expected a second active editor client to be rejected")
	}
}
