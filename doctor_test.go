package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDoctorReportHasFailures(t *testing.T) {
	t.Parallel()

	report := DoctorReport{Checks: []DoctorCheck{{Name: "a", Status: "ok"}, {Name: "b", Status: "warn"}}}
	if report.HasFailures() {
		t.Fatalf("expected no failures")
	}
	report.Checks = append(report.Checks, DoctorCheck{Name: "c", Status: "fail"})
	if !report.HasFailures() {
		t.Fatalf("expected failures")
	}
}

func TestCheckFileStoreConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := filepath.Join(root, "config.toml")

	missingReq := checkFileStoreConfig("req", cfg, true)
	if missingReq.Status != "fail" {
		t.Fatalf("expected fail for required missing config, got %s", missingReq.Status)
	}

	if err := os.WriteFile(cfg, []byte("model = \"o4\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	bad := checkFileStoreConfig("req", cfg, true)
	if bad.Status != "fail" {
		t.Fatalf("expected fail for missing file-store setting, got %s", bad.Status)
	}

	if err := os.WriteFile(cfg, []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	ok := checkFileStoreConfig("req", cfg, true)
	if ok.Status != "ok" {
		t.Fatalf("expected ok for file-store config, got %s", ok.Status)
	}
}

func TestRunDoctorMinimal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICODEX_HOME", filepath.Join(root, "multi"))
	t.Setenv("MULTICODEX_DEFAULT_CODEX_HOME", filepath.Join(root, "codex"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	store := NewStore(paths)
	if err := store.EnsureBaseDirs(); err != nil {
		t.Fatalf("EnsureBaseDirs: %v", err)
	}

	cfg := DefaultConfig()
	report := RunDoctor(store, cfg, 50*time.Millisecond)
	if len(report.Checks) == 0 {
		t.Fatalf("expected non-empty checks")
	}
}

func TestCheckAuthFileStructured(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	content := `{"tokens":{"access_token":"a","refresh_token":"r","id_token":"i"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	check := checkAuthFile("profile test auth", path)
	if check.Status != "ok" {
		t.Fatalf("expected ok, got %s (%s)", check.Status, check.Details)
	}
}

func TestCheckAuthFileInvalidJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	check := checkAuthFile("profile test auth", path)
	if check.Status != "fail" {
		t.Fatalf("expected fail, got %s", check.Status)
	}
	if !strings.Contains(check.Details, "invalid JSON") {
		t.Fatalf("unexpected details: %s", check.Details)
	}
}

func TestCheckAuthFileTokensAndAPIKeyAllowed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	content := `{"OPENAI_API_KEY":"test_api_key_value","tokens":{"access_token":"a","refresh_token":"r","id_token":"i"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	check := checkAuthFile("profile test auth", path)
	if check.Status != "ok" {
		t.Fatalf("expected ok, got %s (%s)", check.Status, check.Details)
	}
}

func TestMissingIgnorePatterns(t *testing.T) {
	t.Parallel()

	full := strings.Join([]string{
		"multicodex/",
		".codex/",
		"**/auth.json",
		".env",
		".env.*",
	}, "\n")
	if got := missingIgnorePatterns(full); len(got) != 0 {
		t.Fatalf("expected no missing patterns, got %v", got)
	}

	minimal := ".multicodex/\n"
	got := missingIgnorePatterns(minimal)
	want := []string{".codex/", "**/auth.json", ".env", ".env.*"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected missing patterns. got=%v want=%v", got, want)
	}

	minimalNew := "multicodex/\n"
	got = missingIgnorePatterns(minimalNew)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected missing patterns for new dir marker. got=%v want=%v", got, want)
	}
}

func TestIsSensitiveTrackedPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path      string
		sensitive bool
	}{
		{path: ".multicodex/config.json", sensitive: true},
		{path: "multicodex/config.json", sensitive: true},
		{path: "multicodex/profiles/work/codex-home/config.toml", sensitive: true},
		{path: "multicodex/backups/default-auth.backup", sensitive: true},
		{path: "multicodex/docs/readme.md", sensitive: false},
		{path: "foo/.codex/auth.json", sensitive: true},
		{path: "auth.json", sensitive: true},
		{path: ".env", sensitive: true},
		{path: ".env.local", sensitive: true},
		{path: ".env.example", sensitive: false},
		{path: "keys/prod.pem", sensitive: true},
		{path: "docs/readme.md", sensitive: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isSensitiveTrackedPath(tc.path); got != tc.sensitive {
				t.Fatalf("unexpected sensitivity for %q: got=%v want=%v", tc.path, got, tc.sensitive)
			}
		})
	}
}

func TestIsSubpathWithSymlinkAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realRoot := filepath.Join(root, "real-root")
	if err := os.MkdirAll(filepath.Join(realRoot, "child"), 0o755); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	aliasRoot := filepath.Join(root, "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatalf("symlink alias root: %v", err)
	}

	if !isSubpath(aliasRoot, filepath.Join(realRoot, "child")) {
		t.Fatalf("expected child under symlink alias root to be detected as subpath")
	}
	if isSubpath(aliasRoot, root) {
		t.Fatalf("expected temp root to not be subpath of alias root")
	}
}
