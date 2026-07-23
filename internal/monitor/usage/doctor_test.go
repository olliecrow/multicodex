package usage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		report DoctorReport
		want   string
	}{
		{
			name: "healthy",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "oauth fetch: personal", OK: true},
				{Name: "oauth fetch: work", OK: true},
			}},
			want: "healthy",
		},
		{
			name: "degraded",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "oauth fetch: personal", OK: true},
				{Name: "oauth fetch: work", OK: false},
			}},
			want: "degraded",
		},
		{
			name: "degraded with setup failure",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "codex binary", OK: false},
				{Name: "oauth fetch: personal", OK: true},
			}},
			want: "degraded",
		},
		{
			name: "failed",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "account candidates", OK: true},
				{Name: "oauth fetch: personal", OK: false},
			}},
			want: "failed",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.report.Status(); got != tc.want {
				t.Fatalf("Status() = %q, want %q", got, tc.want)
			}
			if got := tc.report.Healthy(); got != (tc.want != "failed") {
				t.Fatalf("Healthy() = %v, want %v", got, tc.want != "failed")
			}
		})
	}
}

func TestCheckSourceFetchFormatsWeeklyUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		weekly WindowSummary
		want   string
	}{
		{name: "unused", weekly: WindowSummary{UsedPercent: 0}, want: "plan=pro weekly=0% source=app-server"},
		{name: "partly used", weekly: WindowSummary{UsedPercent: 24}, want: "plan=pro weekly=24% source=app-server"},
		{name: "exhausted", weekly: WindowSummary{UsedPercent: 100}, want: "plan=pro weekly=100% source=app-server"},
		{name: "unavailable", weekly: unavailableWindowSummary(), want: "plan=pro weekly=unavailable source=app-server"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := checkSourceFetch(context.Background(), MonitorAccount{Label: "personal"}, &fakeSource{
				name: "app-server",
				out: &Summary{
					PlanType:     "pro",
					Source:       "app-server",
					WeeklyWindow: tc.weekly,
				},
			})

			if !check.OK {
				t.Fatalf("expected successful check, got %q", check.Details)
			}
			if check.Details != tc.want {
				t.Fatalf("Details = %q, want %q", check.Details, tc.want)
			}
		})
	}
}

func TestCheckCodexBinaryScrubsCodexEnvironment(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(root, "codex-env.log")
	script := "#!/bin/sh\nenv > \"$USAGE_TEST_ENV_LOG\"\nprintf 'codex-test-version\\n'\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("USAGE_TEST_ENV_LOG", logPath)
	t.Setenv("CODEX_HOME", filepath.Join(root, "stale-codex"))
	t.Setenv("MULTICODEX_ACTIVE_PROFILE", "stale")
	t.Setenv("OPENAI_API_KEY", "stale")
	t.Setenv("CODEX_AUTH_TOKEN", "stale")

	check := checkCodexBinary(context.Background())
	if !check.OK {
		t.Fatalf("expected codex binary check ok, got %s", check.Details)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read codex env log: %v", err)
	}
	log := string(data)
	for _, forbidden := range []string{"CODEX_HOME", "MULTICODEX_ACTIVE_PROFILE", "OPENAI_API_KEY", "CODEX_AUTH_TOKEN"} {
		if envLogContainsKey(log, forbidden) {
			t.Fatalf("expected %s to be scrubbed from codex version env", forbidden)
		}
	}
}

func envLogContainsKey(log, key string) bool {
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return true
		}
	}
	return false
}
