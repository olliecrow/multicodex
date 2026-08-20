package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadAttachmentAcceptsPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, extension, err := ReadAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fixture" || extension != ".pdf" {
		t.Fatalf("unexpected attachment: %q, %q", data, extension)
	}
}

func TestReadAttachmentRejectsRelativeAndSymlinkPaths(t *testing.T) {
	if _, _, err := ReadAttachment("relative.txt"); err == nil {
		t.Fatal("expected relative path rejection")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAttachment(link); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestNormalizeExtensionRejectsPathSyntax(t *testing.T) {
	if got := normalizeExtension("PDF"); got != ".pdf" {
		t.Fatalf("extension = %q", got)
	}
	for _, unsafe := range []string{"../png", ".tar.gz", ".very-long-extension-name"} {
		if got := normalizeExtension(unsafe); got != "" {
			t.Fatalf("unsafe extension %q normalized to %q", unsafe, got)
		}
	}
}
