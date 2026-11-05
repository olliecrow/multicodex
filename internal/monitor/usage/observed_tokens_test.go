package usage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	estimate, err := computeObservedTokenEstimate(context.Background(), home, now, make(chan struct{}, 4))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estimate.Status != observedTokensStatusEstimated {
		t.Fatalf("expected estimated status, got %q", estimate.Status)
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
	t.Cleanup(func() { _ = estimator.Close() })
	_, err := estimator.Estimate("", time.Now().UTC())
	if err == nil {
		t.Fatalf("expected error for missing codex home")
	}
}

func TestObservedEstimatorReturnsUnavailableForInvalidHome(t *testing.T) {
	estimator := newObservedTokenEstimator(0, true)
	t.Cleanup(func() { _ = estimator.Close() })
	_, err := estimator.Estimate(filepath.Join(t.TempDir(), "missing"), time.Now().UTC())
	if err == nil {
		t.Fatalf("expected error for invalid codex home path")
	}
}

func TestObservedEstimatorAsyncWarmupSetsWarmingFlag(t *testing.T) {
	now := time.Now().UTC()
	home := t.TempDir()
	estimator := newObservedTokenEstimator(0, true)
	t.Cleanup(func() { _ = estimator.Close() })

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
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	content += tokenCountJSONLineWithLast(now.Add(-2*time.Hour), 100, 50) + "\n"
	content += tokenCountJSONLineWithLast(now.Add(-90*time.Minute), 100, 50) + "\n"
	content += tokenCountJSONLineWithLast(now.Add(-30*time.Minute), 150, 50) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum1w, _, err := estimateTokensFromFile(context.Background(), path, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum1w.Total != 100 {
		t.Fatalf("expected 100 tokens in weekly window, got %d", sum1w.Total)
	}
}

func TestEstimateTokensFromFileFallsBackToLastWhenTotalResets(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	content += tokenCountJSONLineDetailed(now.Add(-2*time.Hour),
		tokenUsageTotal{TotalTokens: 100, InputTokens: 80, OutputTokens: 20},
		tokenUsageTotal{TotalTokens: 100, InputTokens: 80, OutputTokens: 20},
	) + "\n"
	content += tokenCountJSONLineDetailed(now.Add(-90*time.Minute),
		tokenUsageTotal{TotalTokens: 30, InputTokens: 20, OutputTokens: 10},
		tokenUsageTotal{TotalTokens: 30, InputTokens: 20, OutputTokens: 10},
	) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum1w, _, err := estimateTokensFromFile(context.Background(), path, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum1w.Total != 130 {
		t.Fatalf("expected reset fallback total 130 in weekly window, got %d", sum1w.Total)
	}
}

func TestEstimateTokensFromFileAccumulatesSplitBreakdown(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	content += tokenCountJSONLineDetailed(now.Add(-2*time.Hour),
		tokenUsageTotal{
			TotalTokens:           15,
			InputTokens:           10,
			CachedInputTokens:     4,
			OutputTokens:          5,
			ReasoningOutputTokens: 2,
			CachedOutputTokens:    1,
		},
		tokenUsageTotal{
			TotalTokens:           15,
			InputTokens:           10,
			CachedInputTokens:     4,
			OutputTokens:          5,
			ReasoningOutputTokens: 2,
			CachedOutputTokens:    1,
		},
	) + "\n"
	content += tokenCountJSONLineDetailed(now.Add(-90*time.Minute),
		tokenUsageTotal{
			TotalTokens:           27,
			InputTokens:           18,
			CachedInputTokens:     7,
			OutputTokens:          9,
			ReasoningOutputTokens: 4,
			CachedOutputTokens:    2,
		},
		tokenUsageTotal{
			TotalTokens:           12,
			InputTokens:           8,
			CachedInputTokens:     3,
			OutputTokens:          4,
			ReasoningOutputTokens: 2,
			CachedOutputTokens:    1,
		},
	) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum1w, _, err := estimateTokensFromFile(context.Background(), path, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum1w.Total != 27 || sum1w.Input != 18 || sum1w.CachedInput != 7 || sum1w.Output != 9 || sum1w.ReasoningOutput != 4 || sum1w.CachedOutput != 2 {
		t.Fatalf("unexpected split totals: %+v", sum1w)
	}
	if !sum1w.HasSplit {
		t.Fatalf("expected split flag to be set")
	}
	if !sum1w.HasCachedOutput {
		t.Fatalf("expected cached output flag to be set")
	}
}

func TestEstimateTokensFromFileHandlesLargeJSONLLine(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	largeBlob := strings.Repeat("x", 5*1024*1024)
	content := ""
	content += fmt.Sprintf(
		`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":25},"last_token_usage":{"total_tokens":25}}},"blob":"%s"}`+"\n",
		now.Add(-2*time.Hour).UTC().Format(time.RFC3339Nano),
		largeBlob,
	)
	content += tokenCountJSONLineWithLast(now.Add(-30*time.Minute), 40, 15) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum1w, warnings, err := estimateTokensFromFile(context.Background(), path, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum1w.Total != 40 {
		t.Fatalf("expected 40 tokens in weekly window, got %d", sum1w.Total)
	}
	if len(warnings) != 0 {
		t.Fatalf("did not expect parse warnings, got %+v", warnings)
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

func tokenCountJSONLineDetailed(ts time.Time, total tokenUsageTotal, last tokenUsageTotal) string {
	return fmt.Sprintf(
		`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d,"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d,"cached_output_tokens":%d},"last_token_usage":{"total_tokens":%d,"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d,"cached_output_tokens":%d}}}}`,
		ts.UTC().Format(time.RFC3339Nano),
		total.TotalTokens,
		total.InputTokens,
		total.CachedInputTokens,
		total.OutputTokens,
		total.ReasoningOutputTokens,
		total.CachedOutputTokens,
		last.TotalTokens,
		last.InputTokens,
		last.CachedInputTokens,
		last.OutputTokens,
		last.ReasoningOutputTokens,
		last.CachedOutputTokens,
	)
}

func TestEstimateTokensFromFileSkipsOversizedRecords(t *testing.T) {
	now := time.Date(2026, 2, 26, 20, 0, 0, 0, time.UTC)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	oversized := `{"type":"response_item","blob":"` + strings.Repeat("x", maxJSONLRecordBytes) + `"}`
	content := tokenCountJSONLineWithLast(now.Add(-2*time.Hour), 10, 10) + "\n" +
		oversized + "\n" +
		tokenCountJSONLineWithLast(now.Add(-time.Hour), 30, 20) + "\n" +
		oversized
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write usage file: %v", err)
	}

	sum1w, warnings, err := estimateTokensFromFile(context.Background(), path, cutoff1w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum1w.Total != 30 {
		t.Fatalf("expected records around oversized lines to count 30 tokens, got %d", sum1w.Total)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 2 records over 16 MiB") {
		t.Fatalf("expected one oversized-record warning, got %+v", warnings)
	}
}

func TestObservedRefreshCloseAndPrune(t *testing.T) {
	for _, prune := range []bool{false, true} {
		t.Run(fmt.Sprint(prune), func(t *testing.T) {
			e := newObservedTokenEstimator(time.Minute, true)
			t.Cleanup(func() { _ = e.Close() })
			// Occupy all four shared file slots. Every home must wait on this budget.
			if cap(e.workers) != 4 {
				t.Fatal("unexpected worker limit")
			}
			for i := 0; i < 4; i++ {
				e.workers <- struct{}{}
			}
			var homes []string
			var jobs []*observedRefresh
			for i := 0; i < 2; i++ {
				home := t.TempDir()
				dir := filepath.Join(home, "archived_sessions")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "usage.jsonl"), []byte(tokenCountJSONLine(time.Now(), 20)), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Estimate(home, time.Now()); err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				jobs = append(jobs, e.inflight[home])
				e.mu.Unlock()
				homes = append(homes, home)
			}
			for _, job := range jobs {
				select {
				case <-job.done:
					t.Fatal("home bypassed shared worker budget")
				case <-time.After(20 * time.Millisecond):
				}
			}
			// A removed cached home must also be pruned.
			e.mu.Lock()
			e.cache[homes[0]] = cachedObservedEstimate{}
			e.mu.Unlock()
			finished := make(chan struct{})
			go func() {
				if prune {
					(&Fetcher{observed: e}).replaceAccountFetchers(nil)
				} else {
					_ = (&Fetcher{observed: e}).Close()
				}
				close(finished)
			}()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("refresh cancellation did not join")
			}
			for _, job := range jobs {
				select {
				case <-job.done:
				default:
					t.Fatal("worker not joined")
				}
			}
			e.mu.Lock()
			size, running := len(e.cache), len(e.inflight)
			e.mu.Unlock()
			if size != 0 || running != 0 {
				t.Fatalf("retained state: %d cache, %d jobs", size, running)
			}
			for i := 0; i < 4; i++ {
				<-e.workers
			}
			if !prune {
				if _, err := e.Estimate(homes[0], time.Now()); err == nil {
					t.Fatal("closed estimator restarted")
				}
			} else {
				// The same home can be re-added; canceled work must not overwrite it.
				if _, err := e.Estimate(homes[0], time.Now()); err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				job := e.inflight[homes[0]]
				e.mu.Unlock()
				if job == nil {
					job = &observedRefresh{done: make(chan struct{})}
					close(job.done)
				}
				select {
				case <-job.done:
				case <-time.After(2 * time.Second):
					t.Fatal("re-added home did not refresh")
				}
				e.mu.Lock()
				_, ok := e.cache[homes[0]]
				e.mu.Unlock()
				if !ok {
					t.Fatal("re-added home has no estimate")
				}
			}
		})
	}
}

func TestObservedReadsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(path, []byte(tokenCountJSONLine(time.Now(), 20)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := estimateTokensFromFile(ctx, path, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("read: %v", err)
	}
	if _, _, err := discoverRecentUsageFiles(ctx, t.TempDir(), time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery: %v", err)
	}
}
