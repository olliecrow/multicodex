package editor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

func CaptureClipboardImage(ctx context.Context) ([]byte, string, error) {
	switch runtime.GOOS {
	case "darwin":
		return captureMacClipboardImage(ctx)
	case "linux":
		return captureLinuxClipboardImage(ctx)
	default:
		return nil, "", errors.New("clipboard images are supported on macOS and Linux")
	}
}

func ReadAttachment(path string) ([]byte, string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", errors.New("attachment path must be a clean absolute path on the client")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", errors.New("attachment is not accessible on the client")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", errors.New("inspect attachment on the client")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("attachment must be a regular file, not a link")
	}
	if info.Size() <= 0 || info.Size() > maxAttachment {
		return nil, "", fmt.Errorf("attachment must be between 1 byte and %d MiB", maxAttachment>>20)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAttachment+1))
	if err != nil {
		return nil, "", errors.New("read attachment from the client")
	}
	if len(data) == 0 || len(data) > maxAttachment {
		return nil, "", fmt.Errorf("attachment must be between 1 byte and %d MiB", maxAttachment>>20)
	}
	return data, filepath.Ext(path), nil
}

func captureMacClipboardImage(ctx context.Context) ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "multicodex-editor-clipboard-")
	if err != nil {
		return nil, "", errors.New("create private clipboard staging directory")
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", errors.New("secure clipboard staging directory")
	}
	path := filepath.Join(dir, "clipboard.png")
	script := `on run argv
set destination to POSIX file (item 1 of argv)
set imageData to the clipboard as «class PNGf»
set handle to open for access destination with write permission
try
set eof handle to 0
write imageData to handle
close access handle
on error messageText
try
close access handle
end try
error messageText
end try
end run`
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, path)
	cmd.Stdout = &limitedWriter{remaining: 4 << 10}
	cmd.Stderr = &limitedWriter{remaining: 4 << 10}
	if err := cmd.Run(); err != nil {
		return nil, "", errors.New("clipboard does not contain a readable image")
	}
	data, _, err := ReadAttachment(path)
	if err != nil {
		return nil, "", errors.New("clipboard does not contain a readable image")
	}
	return data, ".png", nil
}

func captureLinuxClipboardImage(ctx context.Context) ([]byte, string, error) {
	commands := [][]string{
		{"wl-paste", "--no-newline", "--type", "image/png"},
		{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
	}
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		var output bytes.Buffer
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Stdout = &limitedWriter{dst: &output, remaining: maxAttachment + 1}
		cmd.Stderr = &limitedWriter{remaining: 4 << 10}
		if err := cmd.Run(); err != nil {
			continue
		}
		if output.Len() == 0 || output.Len() > maxAttachment {
			continue
		}
		return output.Bytes(), ".png", nil
	}
	return nil, "", errors.New("clipboard image unavailable; install wl-paste or xclip yourself, or attach a file")
}
