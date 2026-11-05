package codexstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedOutputStopsAtLimit(t *testing.T) {
	stopped := 0
	out := NewCappedOutput(4, func() { stopped++ })
	if n, err := out.Write([]byte("ab")); n != 2 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := out.Write([]byte("cdef")); n != 2 || !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("overflow write = %d, %v", n, err)
	}
	if n, err := out.Write([]byte("g")); n != 0 || !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("write after overflow = %d, %v", n, err)
	}
	if !out.Exceeded() || out.String() != "abcd" || stopped != 1 {
		t.Fatalf("exceeded=%v content=%q stopped=%d", out.Exceeded(), out.String(), stopped)
	}
}

func TestCodexVersionReturnsOnlyVersionShapedOutput(t *testing.T) {
	const marker = "SYNTHETIC-PRIVATE-MARKER"
	tests := []struct {
		name    string
		script  string
		want    string
		wantErr string
	}{
		{"version", "echo 'codex-cli 0.148.0'\n", "codex-cli 0.148.0", ""},
		{"empty", "exit 0\n", "version output is empty", ""},
		{"control characters", "printf 'codex \\033[2J" + marker + "\\n'\n", "", "not recognized"},
		{"failure", "echo '" + marker + "' >&2\nexit 4\n", "", "exit code 4"},
		{"oversized", "while :; do echo 'codex-cli 0.148.0 padding padding padding'; done\n", "", "exceeded 64 KiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeBin := t.TempDir()
			if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\n"+tt.script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			got, err := CodexVersion(context.Background())
			if tt.wantErr == "" {
				if err != nil || got != tt.want {
					t.Fatalf("CodexVersion = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || strings.Contains(err.Error(), marker) {
				t.Fatalf("CodexVersion error = %v; want %q without raw output", err, tt.wantErr)
			}
		})
	}
}
