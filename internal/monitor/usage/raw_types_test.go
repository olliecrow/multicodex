package usage

import "testing"

func TestNormalizeSummaryAllowsMissingSecondaryWindow(t *testing.T) {
	summary, err := normalizeSummary("oauth", rateLimitSnapshotRaw{
		PlanType: "free",
		Primary: &rateLimitWindowRaw{
			UsedPercent: 7,
		},
		Secondary: nil,
	}, nil, 0, &identityInfo{Email: "user@example.com"}, nil)
	if err != nil {
		t.Fatalf("expected missing secondary window to be allowed, got error: %v", err)
	}
	if !summary.WindowDataAvailable {
		t.Fatalf("expected summary to remain available")
	}
	if summary.PrimaryWindow.UsedPercent != 7 {
		t.Fatalf("expected primary window to stay intact, got %d", summary.PrimaryWindow.UsedPercent)
	}
	if summary.SecondaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected secondary window sentinel, got %d", summary.SecondaryWindow.UsedPercent)
	}
	if summary.AccountEmail != "user@example.com" {
		t.Fatalf("expected identity email to be preserved, got %q", summary.AccountEmail)
	}
}

func TestNormalizeSummaryClassifiesWeeklyOnlyPrimaryByDuration(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID: "codex",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        35,
			WindowDurationMins: intPtr(weeklyWindowMinutes),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex": {
			Primary: &rateLimitWindowRaw{
				UsedPercent:        35,
				WindowDurationMins: intPtr(weeklyWindowMinutes),
			},
		},
	}, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	if summary.PrimaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected missing five-hour window to stay unavailable, got %d", summary.PrimaryWindow.UsedPercent)
	}
	if summary.SecondaryWindow.UsedPercent != 35 {
		t.Fatalf("expected weekly primary response in weekly slot, got %d", summary.SecondaryWindow.UsedPercent)
	}
	window := summary.RateLimitWindows["codex"]
	if window.PrimaryWindow.UsedPercent != unavailableUsedPercent || window.SecondaryWindow.UsedPercent != 35 {
		t.Fatalf("expected per-limit window classification to match summary, got %#v", window)
	}
}

func TestNormalizeSummaryClassifiesReversedWindowsByDuration(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		Primary: &rateLimitWindowRaw{
			UsedPercent:        70,
			WindowDurationMins: intPtr(weeklyWindowMinutes),
		},
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        20,
			WindowDurationMins: intPtr(fiveHourWindowMinutes),
		},
	}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	if summary.PrimaryWindow.UsedPercent != 20 || summary.SecondaryWindow.UsedPercent != 70 {
		t.Fatalf("expected duration-based window order, got five-hour=%d weekly=%d", summary.PrimaryWindow.UsedPercent, summary.SecondaryWindow.UsedPercent)
	}
}

func TestNormalizeSummaryAllowsSecondaryOnlyWindow(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID: "codex",
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        18,
			WindowDurationMins: intPtr(fiveHourWindowMinutes),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex_bengalfox": {
			Secondary: &rateLimitWindowRaw{
				UsedPercent:        42,
				WindowDurationMins: intPtr(weeklyWindowMinutes),
			},
		},
	}, 1, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	if summary.PrimaryWindow.UsedPercent != 18 || summary.SecondaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected secondary-only five-hour window, got five-hour=%d weekly=%d", summary.PrimaryWindow.UsedPercent, summary.SecondaryWindow.UsedPercent)
	}
	spark := summary.RateLimitWindows["codex_bengalfox"]
	if spark.PrimaryWindow.UsedPercent != unavailableUsedPercent || spark.SecondaryWindow.UsedPercent != 42 {
		t.Fatalf("expected secondary-only per-limit weekly window, got %#v", spark)
	}
}

func TestNormalizeSummaryMergesClassifiedSlotsAcrossResponseObjects(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID: "codex",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        12,
			WindowDurationMins: intPtr(fiveHourWindowMinutes),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex": {
			Primary: &rateLimitWindowRaw{
				UsedPercent:        34,
				WindowDurationMins: intPtr(weeklyWindowMinutes),
			},
		},
	}, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	window := summary.RateLimitWindows["codex"]
	if window.PrimaryWindow.UsedPercent != 12 || window.SecondaryWindow.UsedPercent != 34 {
		t.Fatalf("expected classified slots from both response objects, got %#v", window)
	}
}

func TestNormalizeSummaryPrefersDeclaredDurationOverPositionalFallback(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		Primary: &rateLimitWindowRaw{
			UsedPercent:        34,
			WindowDurationMins: intPtr(weeklyWindowMinutes),
		},
		Secondary: &rateLimitWindowRaw{UsedPercent: 99},
	}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	if summary.PrimaryWindow.UsedPercent != unavailableUsedPercent || summary.SecondaryWindow.UsedPercent != 34 {
		t.Fatalf("expected declared weekly duration to win, got five-hour=%d weekly=%d", summary.PrimaryWindow.UsedPercent, summary.SecondaryWindow.UsedPercent)
	}
}

func TestNormalizeSummaryPrefersDeclaredDurationAcrossResponseObjects(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID: "codex",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        34,
			WindowDurationMins: intPtr(weeklyWindowMinutes),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex": {Secondary: &rateLimitWindowRaw{UsedPercent: 99}},
	}, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	window := summary.RateLimitWindows["codex"]
	if window.PrimaryWindow.UsedPercent != unavailableUsedPercent || window.SecondaryWindow.UsedPercent != 34 {
		t.Fatalf("expected declared weekly duration to win across response objects, got %#v", window)
	}
}

func TestNormalizeSummaryBuildsRateLimitWindowsFromPrimaryAndAdditionalLimits(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        10,
			WindowDurationMins: intPtr(300),
		},
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        25,
			WindowDurationMins: intPtr(10080),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex_bengalfox": {
			LimitName: stringPtr("Spark"),
			Primary: &rateLimitWindowRaw{
				UsedPercent:        45,
				WindowDurationMins: intPtr(300),
			},
			Secondary: &rateLimitWindowRaw{
				UsedPercent:        55,
				WindowDurationMins: intPtr(10080),
			},
		},
	}, 1, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	if got := len(summary.RateLimitWindows); got != 2 {
		t.Fatalf("expected 2 rate-limit windows, got %d", got)
	}
	if summary.RateLimitWindows["codex"].PrimaryWindow.UsedPercent != 10 {
		t.Fatalf("expected codex primary used percent 10, got %d", summary.RateLimitWindows["codex"].PrimaryWindow.UsedPercent)
	}
	if summary.RateLimitWindows["codex_bengalfox"].LimitName != "Spark" {
		t.Fatalf("expected spark limit name Spark, got %q", summary.RateLimitWindows["codex_bengalfox"].LimitName)
	}
}

func TestNormalizeSummaryMergesPrimaryAndAdditionalLimitWindowData(t *testing.T) {
	primaryLimitName := "Default Plan"
	limitWindows := map[string]rateLimitSnapshotRaw{
		"codex": {
			LimitName: nil,
			Primary: &rateLimitWindowRaw{
				UsedPercent:        90,
				WindowDurationMins: intPtr(300),
			},
			Secondary: nil,
		},
		"  codex_bengalfox  ": {
			LimitName: stringPtr("Spark"),
			Primary: &rateLimitWindowRaw{
				UsedPercent:        55,
				WindowDurationMins: intPtr(300),
			},
			Secondary: &rateLimitWindowRaw{
				UsedPercent:        65,
				WindowDurationMins: intPtr(10080),
			},
		},
	}

	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID:   "codex",
		LimitName: &primaryLimitName,
		PlanType:  "pro",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        10,
			WindowDurationMins: intPtr(300),
		},
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        20,
			WindowDurationMins: intPtr(10080),
		},
	}, limitWindows, 1, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}

	if got := len(summary.RateLimitWindows); got != 2 {
		t.Fatalf("expected 2 rate-limit windows, got %d", got)
	}
	if got := summary.RateLimitWindows["codex"].PrimaryWindow.UsedPercent; got != 90 {
		t.Fatalf("expected primary codex usage from additional map, got %d", got)
	}
	if got := summary.RateLimitWindows["codex"].SecondaryWindow.UsedPercent; got != 20 {
		t.Fatalf("expected secondary codex usage fallback from snapshot, got %d", got)
	}
	if got := summary.RateLimitWindows["codex"].LimitName; got != "Default Plan" {
		t.Fatalf("expected missing limit name to be filled from primary snapshot, got %q", got)
	}
	if got := summary.RateLimitWindows["codex_bengalfox"].LimitName; got != "Spark" {
		t.Fatalf("expected trimmed additional key to normalize and keep limit name, got %q", got)
	}
}

func TestNormalizeSummarySkipsInvalidRateLimitWindowEntries(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        10,
			WindowDurationMins: intPtr(300),
		},
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        20,
			WindowDurationMins: intPtr(10080),
		},
	}, map[string]rateLimitSnapshotRaw{
		"": {
			LimitName: stringPtr("blank-id"),
			Primary:   &rateLimitWindowRaw{UsedPercent: 40},
		},
		"codex_bengalfox": {
			LimitName: stringPtr("Spark"),
			Primary:   nil,
		},
	}, 0, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}

	if got := len(summary.RateLimitWindows); got != 1 {
		t.Fatalf("expected only the base limit window to remain, got %d", got)
	}
	if _, ok := summary.RateLimitWindows["codex_bengalfox"]; ok {
		t.Fatalf("expected codex_bengalfox window without usage data to be skipped")
	}
}

func TestNormalizeSummaryAllowsSparkSelectionByLimitIDAndLimitName(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: &rateLimitWindowRaw{
			UsedPercent:        10,
			WindowDurationMins: intPtr(300),
		},
		Secondary: &rateLimitWindowRaw{
			UsedPercent:        20,
			WindowDurationMins: intPtr(10080),
		},
	}, map[string]rateLimitSnapshotRaw{
		"codex_bengalfox": {
			LimitName: stringPtr("Spark Burst"),
			Primary: &rateLimitWindowRaw{
				UsedPercent:        55,
				WindowDurationMins: intPtr(300),
			},
			Secondary: &rateLimitWindowRaw{
				UsedPercent:        65,
				WindowDurationMins: intPtr(10080),
			},
		},
		"  another-window  ": {
			LimitName: stringPtr("fast"),
			Primary: &rateLimitWindowRaw{
				UsedPercent: 80,
			},
			Secondary: &rateLimitWindowRaw{
				UsedPercent: 90,
			},
		},
	}, 2, nil, nil)
	if err != nil {
		t.Fatalf("normalizeSummary failed: %v", err)
	}
	_, sparkWindow, ok := summary.RateLimitWindowForModel("gpt-5-codex-spark")
	if !ok {
		t.Fatalf("expected spark model to resolve to a rate limit bucket")
	}
	if sparkWindow.LimitID != "codex_bengalfox" {
		t.Fatalf("expected spark bucket selection to prefer codex_bengalfox, got %q", sparkWindow.LimitID)
	}
	if sparkWindow.PrimaryWindow.UsedPercent != 55 {
		t.Fatalf("expected spark primary usage 55, got %d", sparkWindow.PrimaryWindow.UsedPercent)
	}
}

func TestRateLimitWindowForModelDoesNotFallbackToCodexForSparkModel(t *testing.T) {
	summary := &Summary{
		RateLimitWindows: map[string]RateLimitWindow{
			"codex": {
				LimitID:       "codex",
				PrimaryWindow: WindowSummary{UsedPercent: 12},
			},
		},
	}

	if _, _, ok := summary.RateLimitWindowForModel("gpt-5-codex-spark"); ok {
		t.Fatalf("expected spark model lookup not to use default codex window")
	}
	if _, window, ok := summary.RateLimitWindowForModel("gpt-5-codex"); !ok || window.PrimaryWindow.UsedPercent != 12 {
		t.Fatalf("expected non-spark model lookup to use default codex window")
	}
}

func intPtr(v int) *int { return &v }

func stringPtr(v string) *string { return &v }
