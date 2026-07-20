package usage

import (
	"errors"
	"strings"
	"testing"
)

func TestSelectBestAccountOrdersByPriorityThenWeeklyReset(t *testing.T) {
	results := []accountFetchResult{
		selectionResult("reserve", 100, 20, 60),
		selectionResult("later", 0, 10, 3600),
		selectionResult("sooner", 0, 90, 120),
	}
	selected, err := selectBestAccountFromResultsForModel(results, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "sooner" || selected.WeeklyUsedPercent != 90 {
		t.Fatalf("expected sooner configured account, got %+v", selected)
	}
}

func TestSelectBestAccountUsesKnownResetBeforeUnknown(t *testing.T) {
	known := selectionResult("known", 0, 70, 600)
	unknown := selectionResult("unknown", 0, 5, -1)
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{unknown, known}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "known" {
		t.Fatalf("expected known reset before unknown, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountRandomizesOnlyExactTies(t *testing.T) {
	original := chooseRandomResultIndex
	defer func() { chooseRandomResultIndex = original }()
	calls := 0
	chooseRandomResultIndex = func(candidates []int) int {
		calls++
		return candidates[len(candidates)-1]
	}

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("first", 0, 10, 600),
		selectionResult("second", 0, 20, 600),
		selectionResult("later", 0, 1, 601),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "second" || calls != 1 {
		t.Fatalf("expected one random choice among exact ties, got %+v calls=%d", selected, calls)
	}
}

func TestSelectBestAccountRandomizesUnknownResetTies(t *testing.T) {
	original := chooseRandomResultIndex
	defer func() { chooseRandomResultIndex = original }()
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) == 0 {
			return -1
		}
		return candidates[len(candidates)-1]
	}

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("first", 0, 10, -1),
		selectionResult("second", 0, 20, -1),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "second" {
		t.Fatalf("expected random unknown-reset tie choice, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountSkipsMissingAndExhaustedWeeklyUsage(t *testing.T) {
	missing := selectionResult("missing", 0, unavailableUsedPercent, -1)
	exhausted := selectionResult("exhausted", 0, 100, 60)
	usable := selectionResult("usable", 0, 99, 600)
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{missing, exhausted, usable}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "usable" {
		t.Fatalf("expected usable weekly account, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesProtectedReserveFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile accountFetchResult
	}{
		{name: "exhausted", profile: selectionResult("profile", 0, 100, 60)},
		{name: "missing", profile: selectionResult("profile", 0, unavailableUsedPercent, -1)},
		{name: "fetch failed", profile: accountFetchResult{codexHome: "/profile", account: AccountSummary{Label: "profile"}, fetchErr: errors.New("failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reserve := selectionResult("default", 100, unavailableUsedPercent, -1)
			selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{tc.profile, reserve}, "")
			if err != nil {
				t.Fatal(err)
			}
			if selected.Account.Label != "default" || selected.WeeklyUsedPercent != unavailableUsedPercent {
				t.Fatalf("expected protected reserve fallback, got %+v", selected)
			}
		})
	}
}

func TestSelectBestAccountKeepsReserveBehindUsableProfile(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("default", 100, 1, 60),
		selectionResult("profile", 0, 99, -1),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "profile" {
		t.Fatalf("expected configured profile before reserve, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountWeeklyErrors(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("missing", 0, unavailableUsedPercent, -1),
	}, "")
	if err == nil || !strings.Contains(err.Error(), "weekly") {
		t.Fatalf("expected weekly-only unavailable error, got %v", err)
	}

	_, err = selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("exhausted", 0, 100, 60),
	}, "")
	if err == nil || err.Error() != "no accounts with remaining weekly usage" {
		t.Fatalf("expected weekly exhaustion error, got %v", err)
	}
}

func TestSelectBestAccountForSparkUsesSparkWeeklyWindow(t *testing.T) {
	standard := selectionResult("standard", 0, 10, 60)
	standard.account.RateLimitWindows = map[string]RateLimitWindow{
		"codex_bengalfox": {LimitName: "Spark", WeeklyWindow: weeklyWindow(90, 300)},
	}
	spark := selectionResult("spark", 0, 90, 60)
	spark.account.RateLimitWindows = map[string]RateLimitWindow{
		"codex_bengalfox": {LimitName: "Spark", WeeklyWindow: weeklyWindow(10, 120)},
	}

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{standard, spark}, "gpt-5.3-codex-spark")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "spark" || selected.WeeklyUsedPercent != 10 {
		t.Fatalf("expected Spark weekly routing, got %+v", selected)
	}
}

func TestSelectBestAccountForSparkRequiresSparkWeeklyWindow(t *testing.T) {
	withoutSpark := selectionResult("standard", 0, 10, 60)
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{withoutSpark}, "spark")
	if err == nil || !strings.Contains(err.Error(), "model-specific weekly limit") {
		t.Fatalf("expected missing Spark weekly error, got %v", err)
	}

	withMissingSpark := selectionResult("missing", 0, 10, 60)
	withMissingSpark.account.RateLimitWindows = map[string]RateLimitWindow{
		"spark": {LimitName: "Spark", WeeklyWindow: unavailableWindowSummary()},
	}
	_, err = selectBestAccountFromResultsForModel([]accountFetchResult{withMissingSpark}, "spark")
	if err == nil || !strings.Contains(err.Error(), "model-eligible") {
		t.Fatalf("expected unusable Spark weekly error, got %v", err)
	}
}

func TestSelectBestAccountForSparkCanUseReserveFallback(t *testing.T) {
	profile := selectionResult("profile", 0, 20, 60)
	reserve := selectionResult("default", 100, 10, 60)
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{profile, reserve}, "spark")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "default" || selected.WeeklyUsedPercent != unavailableUsedPercent {
		t.Fatalf("expected default reserve for missing Spark buckets, got %+v", selected)
	}
}

func selectionResult(label string, priority, used int, resetSeconds int64) accountFetchResult {
	window := WindowSummary{UsedPercent: used}
	if resetSeconds >= 0 {
		window.SecondsUntilReset = &resetSeconds
	}
	summary := &Summary{WeeklyWindow: window}
	return accountFetchResult{
		codexHome:         "/" + label,
		selectionPriority: priority,
		account: AccountSummary{
			Label:        label,
			WeeklyWindow: window,
		},
		snapshot: summary,
	}
}

func weeklyWindow(used int, resetSeconds int64) WindowSummary {
	return WindowSummary{UsedPercent: used, SecondsUntilReset: &resetSeconds}
}
