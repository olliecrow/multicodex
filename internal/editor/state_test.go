package editor

import (
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
