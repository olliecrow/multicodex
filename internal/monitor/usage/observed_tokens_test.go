package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeObservedTokenEstimate(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	home := t.TempDir()

	todayDir := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(todayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sessionPath := filepath.Join(todayDir, "session-a.jsonl")
	sessionContent := ""
	sessionContent += tokenCountJSONLine(now.Add(-6*time.Hour), 100) + "\n"
	sessionContent += tokenCountJSONLine(now.Add(-4*time.Hour), 140) + "\n"
	sessionContent += "not-json\n"
	sessionContent += tokenCountJSONLine(now.Add(-2*time.Hour), 200) + "\n"
	sessionContent += tokenCountJSONLine(now.Add(-30*time.Minute), 260) + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	archivedDir := filepath.Join(home, "archived_sessions")
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		t.Fatalf("mkdir archived: %v", err)
	}
	archivedPath := filepath.Join(archivedDir, "archived-a.jsonl")
	archivedContent := ""
	archivedContent += tokenCountJSONLine(now.Add(-3*24*time.Hour), 20) + "\n"
	archivedContent += tokenCountJSONLine(now.Add(-2*24*time.Hour), 50) + "\n"
	if err := os.WriteFile(archivedPath, []byte(archivedContent), 0o600); err != nil {
		t.Fatalf("write archived file: %v", err)
	}
	if err := os.Chtimes(archivedPath, now, now); err != nil {
		t.Fatalf("chtimes archived file: %v", err)
	}

	estimate, err := computeObservedTokenEstimate(home, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estimate.Status != observedTokensStatusEstimated {
		t.Fatalf("expected estimated status, got %q", estimate.Status)
	}
	if estimate.Window5h.Total != 160 {
		t.Fatalf("expected 5h tokens 160, got %d", estimate.Window5h.Total)
	}
	if estimate.WindowWeekly.Total != 190 {
		t.Fatalf("expected weekly tokens 190, got %d", estimate.WindowWeekly.Total)
	}
	if len(estimate.Warnings) == 0 {
		t.Fatalf("expected parse warning for invalid json line")
	}
}

func TestObservedEstimatorReturnsUnavailableForMissingHome(t *testing.T) {
	estimator := newObservedTokenEstimator(0, true)
	_, err := estimator.Estimate("", time.Now().UTC())
	if err == nil {
		t.Fatalf("expected error for missing codex home")
	}
}

func TestObservedEstimatorReturnsUnavailableForInvalidHome(t *testing.T) {
	estimator := newObservedTokenEstimator(0, true)
	_, err := estimator.Estimate(filepath.Join(t.TempDir(), "missing"), time.Now().UTC())
	if err == nil {
		t.Fatalf("expected error for invalid codex home path")
	}
}

func TestObservedEstimatorAsyncWarmupSetsWarmingFlag(t *testing.T) {
	now := time.Now().UTC()
	home := t.TempDir()
	estimator := newObservedTokenEstimator(0, true)

	estimate, err := estimator.Estimate(home, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estimate.Status != observedTokensStatusUnavailable {
		t.Fatalf("expected unavailable status during async warmup, got %q", estimate.Status)
	}
	if !estimate.Warming {
		t.Fatalf("expected warming flag during async warmup")
	}
}

func TestEstimateTokensFromFileDoesNotDoubleCountDuplicateTotals(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	cutoff5h := now.Add(-5 * time.Hour)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	content += tokenCountJSONLineWithLast(now.Add(-2*time.Hour), 100, 50) + "\n"
	content += tokenCountJSONLineWithLast(now.Add(-90*time.Minute), 100, 50) + "\n"
	content += tokenCountJSONLineWithLast(now.Add(-30*time.Minute), 150, 50) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum5h, sum1w, _, err := estimateTokensFromFile(path, cutoff5h, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum5h.Total != 100 {
		t.Fatalf("expected 100 tokens in 5h window, got %d", sum5h.Total)
	}
	if sum1w.Total != 100 {
		t.Fatalf("expected 100 tokens in weekly window, got %d", sum1w.Total)
	}
}

func tokenCountJSONLine(ts time.Time, total int64) string {
	return fmt.Sprintf(
		`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d}}}}`,
		ts.UTC().Format(time.RFC3339Nano),
		total,
	)
}

func tokenCountJSONLineWithLast(ts time.Time, total int64, last int64) string {
	return fmt.Sprintf(
		`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":{"total_tokens":%d}}}}`,
		ts.UTC().Format(time.RFC3339Nano),
		total,
		last,
	)
}
