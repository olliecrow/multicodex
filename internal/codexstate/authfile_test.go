package codexstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadAuthFile(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "auth.json")
	if err := os.WriteFile(regular, []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, info, err := ReadAuthFile(regular); err != nil || string(data) != `{"tokens":{}}` || !info.Mode().IsRegular() {
		t.Fatalf("regular auth file = %q, %v", data, err)
	}

	if _, _, err := ReadAuthFile(filepath.Join(root, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing auth file error = %v", err)
	}

	link := filepath.Join(root, "link.json")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	expectAuthFileError(t, link, "symlink")

	hardLink := filepath.Join(root, "hard.json")
	if err := os.Link(regular, hardLink); err != nil {
		t.Fatal(err)
	}
	expectAuthFileError(t, hardLink, "multiple hard links")

	fifo := filepath.Join(root, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	expectAuthFileError(t, fifo, "not a regular file")

	large := filepath.Join(root, "large.json")
	if err := os.WriteFile(large, make([]byte, MaxAuthFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	expectAuthFileError(t, large, "exceeds")
}

func expectAuthFileError(t *testing.T, path, want string) {
	t.Helper()
	if _, _, err := ReadAuthFile(path); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ReadAuthFile(%s) error = %v, want %q", filepath.Base(path), err, want)
	}
}

func TestBoundedConfigReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	for _, read := range []func(string) ([]byte, error){ReadConfigFile, ReadMetadataFile} {
		if err := os.WriteFile(path, make([]byte, MaxConfigFileBytes), 0600); err != nil {
			t.Fatal(err)
		}
		if data, err := read(path); err != nil || len(data) != MaxConfigFileBytes {
			t.Fatalf("limit: length %d, %v", len(data), err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write([]byte("x"))
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if data, err := read(path); err == nil || data != nil {
			t.Fatal("oversized file accepted")
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.WriteFile(path, []byte("config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfigFile(link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadataFile(link); err == nil {
		t.Fatal("metadata symlink accepted")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfigFile(fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestConfigOpenErrorReportsReasonWithoutContents(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores mode 000")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	marker := "contents-must-stay-out-of-the-error"
	if err := os.WriteFile(path, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func(string) ([]byte, error){ReadConfigFile, ReadMetadataFile} {
		_, err := read(path)
		if err == nil || !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), path) {
			t.Fatalf("error = %v", err)
		}
	}
}
