package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	observedTokensStatusEstimated   = "estimated"
	observedTokensStatusPartial     = "partial"
	observedTokensStatusUnavailable = "unavailable"
)

type ObservedTokenEstimate struct {
	Window5h     ObservedTokenBreakdown
	WindowWeekly ObservedTokenBreakdown
	Status       string
	Warming      bool
	Note         string
	Warnings     []string
}

type observedTokenEstimator struct {
	mu       sync.Mutex
	cache    map[string]cachedObservedEstimate
	ttl      time.Duration
	async    bool
	inflight map[string]struct{}
}

type cachedObservedEstimate struct {
	at       time.Time
	estimate ObservedTokenEstimate
}

type tokenCountLine struct {
	Timestamp string                `json:"timestamp"`
	Type      string                `json:"type"`
	Payload   tokenCountLinePayload `json:"payload"`
}

type tokenCountLinePayload struct {
	Type string          `json:"type"`
	Info *tokenCountInfo `json:"info"`
}

type tokenCountInfo struct {
	Total tokenUsageTotal `json:"total_token_usage"`
	Last  tokenUsageTotal `json:"last_token_usage"`
}

type tokenUsageTotal struct {
	TotalTokens           int64 `json:"total_tokens"`
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	CachedOutputTokens    int64 `json:"cached_output_tokens"`
}

type ObservedTokenBreakdown struct {
	Total           int64 `json:"total"`
	Input           int64 `json:"input,omitempty"`
	CachedInput     int64 `json:"cached_input,omitempty"`
	Output          int64 `json:"output,omitempty"`
	ReasoningOutput int64 `json:"reasoning_output,omitempty"`
	CachedOutput    int64 `json:"cached_output,omitempty"`
	HasSplit        bool  `json:"has_split,omitempty"`
	HasCachedOutput bool  `json:"has_cached_output,omitempty"`
}

type tokenAccumulator struct {
	Total           int64
	Input           int64
	CachedInput     int64
	Output          int64
	ReasoningOutput int64
	CachedOutput    int64
	HasSplit        bool
	HasCachedOutput bool
}

type observedWindowPair struct {
	Window5h     ObservedTokenBreakdown
	WindowWeekly ObservedTokenBreakdown
}

func newObservedTokenEstimator(ttl time.Duration, async bool) *observedTokenEstimator {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &observedTokenEstimator{
		cache:    map[string]cachedObservedEstimate{},
		ttl:      ttl,
		async:    async,
		inflight: map[string]struct{}{},
	}
}

func (e *observedTokenEstimator) Estimate(codexHome string, now time.Time) (ObservedTokenEstimate, error) {
	trimmedHome := strings.TrimSpace(codexHome)
	if trimmedHome == "" {
		return ObservedTokenEstimate{
			Status: observedTokensStatusUnavailable,
			Note:   "missing codex home",
		}, errors.New("missing codex home")
	}
	home := filepath.Clean(trimmedHome)
	now = now.UTC()

	info, err := os.Stat(home)
	if err != nil {
		return ObservedTokenEstimate{
			Status: observedTokensStatusUnavailable,
			Note:   fmt.Sprintf("codex home is not accessible: %v", err),
		}, fmt.Errorf("stat codex home %s: %w", home, err)
	}
	if !info.IsDir() {
		return ObservedTokenEstimate{
			Status: observedTokensStatusUnavailable,
			Note:   "codex home is not a directory",
		}, fmt.Errorf("codex home %s is not a directory", home)
	}

	e.mu.Lock()
	cached, hasCached := e.cache[home]
	if hasCached && now.Sub(cached.at) <= e.ttl {
		e.mu.Unlock()
		out := cached.estimate
		out.Note = "local estimate (updated " + humanDuration(now.Sub(cached.at)) + " ago)"
		return out, nil
	}
	if !e.async {
		e.mu.Unlock()
		estimate, err := computeObservedTokenEstimate(home, now)
		if err != nil {
			return ObservedTokenEstimate{
				Status: observedTokensStatusUnavailable,
				Note:   err.Error(),
			}, err
		}
		e.mu.Lock()
		e.cache[home] = cachedObservedEstimate{at: now, estimate: estimate}
		e.mu.Unlock()
		return estimate, nil
	}
	if _, running := e.inflight[home]; !running {
		e.inflight[home] = struct{}{}
		go e.refreshAsync(home)
	}
	e.mu.Unlock()

	if hasCached {
		out := cached.estimate
		out.Note = "local estimate (refreshing)"
		return out, nil
	}

	return ObservedTokenEstimate{
		Status:  observedTokensStatusUnavailable,
		Warming: true,
		Note:    "warming token estimate",
	}, nil
}

func (e *observedTokenEstimator) refreshAsync(codexHome string) {
	now := time.Now().UTC()
	estimate, err := computeObservedTokenEstimate(codexHome, now)
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.inflight, codexHome)
	if err != nil {
		return
	}
	e.cache[codexHome] = cachedObservedEstimate{at: now, estimate: estimate}
}

func computeObservedTokenEstimate(codexHome string, now time.Time) (ObservedTokenEstimate, error) {
	files, warnings, err := discoverRecentUsageFiles(codexHome, now)
	if err != nil {
		return ObservedTokenEstimate{}, err
	}

	cutoff5h := now.Add(-5 * time.Hour)
	cutoff1w := now.Add(-7 * 24 * time.Hour)

	total5h, totalWeekly, fileWarnings, err := estimateTokensAcrossFiles(files, cutoff5h, cutoff1w)
	if err != nil {
		return ObservedTokenEstimate{}, err
	}
	warnings = append(warnings, fileWarnings...)

	return ObservedTokenEstimate{
		Window5h:     total5h.toBreakdown(),
		WindowWeekly: totalWeekly.toBreakdown(),
		Status:       observedTokensStatusEstimated,
		Note:         "local estimate",
		Warnings:     dedupeStrings(warnings),
	}, nil
}

type fileEstimateResult struct {
	window5h     tokenAccumulator
	windowWeekly tokenAccumulator
	warnings     []string
	err          error
}

func estimateTokensAcrossFiles(files []string, cutoff5h, cutoff1w time.Time) (tokenAccumulator, tokenAccumulator, []string, error) {
	if len(files) == 0 {
		return tokenAccumulator{}, tokenAccumulator{}, nil, nil
	}

	parallelism := len(files)
	if parallelism > 4 {
		parallelism = 4
	}
	if parallelism < 1 {
		parallelism = 1
	}

	jobs := make(chan string)
	results := make(chan fileEstimateResult, parallelism)
	var wg sync.WaitGroup

	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				file5h, fileWeekly, fileWarnings, err := estimateTokensFromFile(file, cutoff5h, cutoff1w)
				results <- fileEstimateResult{
					window5h:     file5h,
					windowWeekly: fileWeekly,
					warnings:     fileWarnings,
					err:          err,
				}
			}
		}()
	}

	go func() {
		for _, file := range files {
			jobs <- file
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var total5h tokenAccumulator
	var totalWeekly tokenAccumulator
	var warnings []string
	var firstErr error

	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		total5h.add(result.window5h)
		totalWeekly.add(result.windowWeekly)
		warnings = append(warnings, result.warnings...)
	}
	if firstErr != nil {
		return tokenAccumulator{}, tokenAccumulator{}, nil, firstErr
	}
	return total5h, totalWeekly, warnings, nil
}

func discoverRecentUsageFiles(codexHome string, now time.Time) ([]string, []string, error) {
	var files []string
	var warnings []string
	cutoff := now.Add(-8 * 24 * time.Hour)

	for day := 0; day <= 8; day++ {
		d := now.AddDate(0, 0, -day)
		dir := filepath.Join(codexHome, "sessions", d.Format("2006"), d.Format("01"), d.Format("02"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, fmt.Errorf("read sessions dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	archivedDir := filepath.Join(codexHome, "archived_sessions")
	entries, err := os.ReadDir(archivedDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("read archived sessions dir %s: %w", archivedDir, err)
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			fullPath := filepath.Join(archivedDir, entry.Name())
			info, infoErr := entry.Info()
			if infoErr != nil {
				warnings = append(warnings, fmt.Sprintf("skip %s: %v", fullPath, infoErr))
				continue
			}
			if info.ModTime().UTC().Before(cutoff) {
				continue
			}
			files = append(files, fullPath)
		}
	}

	sort.Strings(files)
	return files, warnings, nil
}

func estimateTokensFromFile(path string, cutoff5h, cutoff1w time.Time) (tokenAccumulator, tokenAccumulator, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return tokenAccumulator{}, tokenAccumulator{}, nil, fmt.Errorf("open usage file %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var warnings []string
	var prevTotal *tokenUsageTotal
	var sum5h tokenAccumulator
	var sum1w tokenAccumulator
	parseErrCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		var rec tokenCountLine
		if err := json.Unmarshal(line, &rec); err != nil {
			parseErrCount++
			continue
		}
		if rec.Type != "event_msg" || rec.Payload.Type != "token_count" || rec.Payload.Info == nil {
			continue
		}

		eventTime, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil {
			parseErrCount++
			continue
		}
		eventTime = eventTime.UTC()
		if !eventTime.Before(cutoff1w) {
			usage, ok := usageForEvent(rec.Payload.Info.Total, rec.Payload.Info.Last, prevTotal)
			if ok {
				sum1w.addTokenUsage(usage)
				if !eventTime.Before(cutoff5h) {
					sum5h.addTokenUsage(usage)
				}
			}
		}
		current := rec.Payload.Info.Total
		prevTotal = &current
	}

	if err := scanner.Err(); err != nil {
		return tokenAccumulator{}, tokenAccumulator{}, nil, fmt.Errorf("scan usage file %s: %w", path, err)
	}

	if parseErrCount > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d unparsable lines in %s", parseErrCount, filepath.Base(path)))
	}
	return sum5h, sum1w, warnings, nil
}

func usageForEvent(current tokenUsageTotal, last tokenUsageTotal, previous *tokenUsageTotal) (tokenUsageTotal, bool) {
	if previous != nil {
		if delta, ok := tokenUsageDelta(*previous, current); ok {
			if delta.TotalTokens > 0 {
				return delta, true
			}
			return tokenUsageTotal{}, false
		}
	}
	if last.hasUsage() {
		return last, true
	}
	return tokenUsageTotal{}, false
}

func tokenUsageDelta(prev tokenUsageTotal, current tokenUsageTotal) (tokenUsageTotal, bool) {
	if current.TotalTokens < prev.TotalTokens {
		return tokenUsageTotal{}, false
	}
	totalDelta := current.TotalTokens - prev.TotalTokens
	if totalDelta <= 0 {
		return tokenUsageTotal{}, true
	}

	return tokenUsageTotal{
		TotalTokens:           totalDelta,
		InputTokens:           nonNegativeDelta(prev.InputTokens, current.InputTokens),
		CachedInputTokens:     nonNegativeDelta(prev.CachedInputTokens, current.CachedInputTokens),
		OutputTokens:          nonNegativeDelta(prev.OutputTokens, current.OutputTokens),
		ReasoningOutputTokens: nonNegativeDelta(prev.ReasoningOutputTokens, current.ReasoningOutputTokens),
		CachedOutputTokens:    nonNegativeDelta(prev.CachedOutputTokens, current.CachedOutputTokens),
	}, true
}

func nonNegativeDelta(prev, current int64) int64 {
	if current <= prev {
		return 0
	}
	return current - prev
}

func (a *tokenAccumulator) add(other tokenAccumulator) {
	a.Total += other.Total
	a.Input += other.Input
	a.CachedInput += other.CachedInput
	a.Output += other.Output
	a.ReasoningOutput += other.ReasoningOutput
	a.CachedOutput += other.CachedOutput
	a.HasSplit = a.HasSplit || other.HasSplit
	a.HasCachedOutput = a.HasCachedOutput || other.HasCachedOutput
}

func (a *tokenAccumulator) addTotalOnly(total int64) {
	a.Total += total
}

func (a *tokenAccumulator) addTokenUsage(usage tokenUsageTotal) {
	if usage.TotalTokens <= 0 {
		return
	}
	a.Total += usage.TotalTokens
	a.Input += usage.InputTokens
	a.CachedInput += usage.CachedInputTokens
	a.Output += usage.OutputTokens
	a.ReasoningOutput += usage.ReasoningOutputTokens
	a.CachedOutput += usage.CachedOutputTokens
	a.HasSplit = true
	if usage.CachedOutputTokens != 0 {
		a.HasCachedOutput = true
	}
}

func (a tokenAccumulator) toBreakdown() ObservedTokenBreakdown {
	return ObservedTokenBreakdown{
		Total:           a.Total,
		Input:           a.Input,
		CachedInput:     a.CachedInput,
		Output:          a.Output,
		ReasoningOutput: a.ReasoningOutput,
		CachedOutput:    a.CachedOutput,
		HasSplit:        a.HasSplit,
		HasCachedOutput: a.HasCachedOutput,
	}
}

func (t tokenUsageTotal) hasUsage() bool {
	return t.TotalTokens > 0 ||
		t.InputTokens > 0 ||
		t.CachedInputTokens > 0 ||
		t.OutputTokens > 0 ||
		t.ReasoningOutputTokens > 0 ||
		t.CachedOutputTokens > 0
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Second {
		return "0s"
	}
	return d.Round(time.Second).String()
}
