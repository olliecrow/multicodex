package usage

import "testing"

func TestNormalizeSummaryAllowsMissingSecondaryWindow(t *testing.T) {
	summary, err := normalizeSummary("oauth", rateLimitSnapshotRaw{
		PlanType: "free",
		Primary: &rateLimitWindowRaw{
			UsedPercent: 7,
		},
		Secondary: nil,
	}, nil, 0, &identityInfo{Email: "crowoy1@gmail.com"}, nil)
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
	if summary.AccountEmail != "crowoy1@gmail.com" {
		t.Fatalf("expected identity email to be preserved, got %q", summary.AccountEmail)
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
			LimitName: func() *string { v := "Spark"; return &v }(),
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
			LimitName: func() *string { v := "Spark"; return &v }(),
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
			LimitName: func() *string { v := "blank-id"; return &v }(),
			Primary:   &rateLimitWindowRaw{UsedPercent: 40},
		},
		"codex_bengalfox": {
			LimitName: func() *string { v := "Spark"; return &v }(),
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
		t.Fatalf("expected codex_bengalfox window without primary to be skipped")
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
			LimitName: func() *string { v := "Spark Burst"; return &v }(),
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
			LimitName: func() *string { v := "fast"; return &v }(),
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

func intPtr(v int) *int { return &v }
