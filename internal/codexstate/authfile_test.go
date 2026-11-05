package codexstate

import (
	"errors"
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
