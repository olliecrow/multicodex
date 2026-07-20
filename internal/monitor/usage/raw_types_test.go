package usage

import "testing"

func TestNormalizeSummaryExtractsDeclaredWeeklyWindowFromEitherPosition(t *testing.T) {
	for _, tc := range []struct {
		name      string
		primary   *rateLimitWindowRaw
		secondary *rateLimitWindowRaw
		want      int
	}{
		{name: "primary", primary: rawWeeklyWindow(35), want: 35},
		{name: "secondary", secondary: rawWeeklyWindow(42), want: 42},
		{name: "reversed", primary: rawWeeklyWindow(70), secondary: rawWindow(20, 300), want: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
				LimitID: "codex",
				Primary: tc.primary, Secondary: tc.secondary,
			}, nil, 0, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if summary.WeeklyWindow.UsedPercent != tc.want {
				t.Fatalf("unexpected weekly window: %+v", summary.WeeklyWindow)
			}
			if got := summary.RateLimitWindows["codex"].WeeklyWindow.UsedPercent; got != summary.WeeklyWindow.UsedPercent {
				t.Fatalf("expected per-limit weekly window %d, got %d", summary.WeeklyWindow.UsedPercent, got)
			}
		})
	}
}

func TestNormalizeSummaryUsesOnlyNarrowLegacySecondaryFallback(t *testing.T) {
	summary, err := normalizeSummary("oauth", rateLimitSnapshotRaw{
		Primary:   &rateLimitWindowRaw{UsedPercent: 7},
		Secondary: &rateLimitWindowRaw{UsedPercent: 31},
	}, nil, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WeeklyWindow.UsedPercent != 31 {
		t.Fatalf("expected positional secondary weekly fallback, got %+v", summary.WeeklyWindow)
	}

	primaryOnly, err := normalizeSummary("oauth", rateLimitSnapshotRaw{
		Primary: &rateLimitWindowRaw{UsedPercent: 7},
	}, nil, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if primaryOnly.WeeklyWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected undeclared primary-only payload to remain unknown, got %+v", primaryOnly.WeeklyWindow)
	}
	if primaryOnly.WindowDataAvailable {
		t.Fatal("expected undeclared primary-only payload not to report weekly data as available")
	}
}

func TestNormalizeSummaryDeclaredWeeklyWinsOverPositionalFallback(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		Primary:   rawWeeklyWindow(34),
		Secondary: &rateLimitWindowRaw{UsedPercent: 99},
	}, nil, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WeeklyWindow.UsedPercent != 34 {
		t.Fatalf("expected declared weekly duration to win, got %+v", summary.WeeklyWindow)
	}
}

func TestNormalizeSummaryMergesWeeklyWindowAcrossResponseObjects(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID: "codex",
		Primary: rawWeeklyWindow(34),
	}, map[string]rateLimitSnapshotRaw{
		"codex": {Secondary: &rateLimitWindowRaw{UsedPercent: 99}},
	}, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary.RateLimitWindows["codex"].WeeklyWindow.UsedPercent; got != 34 {
		t.Fatalf("expected declared weekly data across objects to win, got %d", got)
	}
}

func TestNormalizeSummaryBuildsDefaultAndSparkWeeklyBuckets(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{
		LimitID:   "codex",
		PlanType:  "pro",
		Primary:   rawWindow(10, 300),
		Secondary: rawWeeklyWindow(25),
	}, map[string]rateLimitSnapshotRaw{
		"codex_bengalfox": {
			LimitName: stringPtr("Spark"),
			Primary:   rawWeeklyWindow(55),
		},
		"ignored": {},
		"":        {Primary: rawWeeklyWindow(99)},
	}, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.RateLimitWindows) != 2 {
		t.Fatalf("expected default and Spark buckets, got %+v", summary.RateLimitWindows)
	}
	if got := summary.RateLimitWindows["codex"].WeeklyWindow.UsedPercent; got != 25 {
		t.Fatalf("expected default weekly 25, got %d", got)
	}
	spark := summary.RateLimitWindows["codex_bengalfox"]
	if spark.LimitName != "Spark" || spark.WeeklyWindow.UsedPercent != 55 {
		t.Fatalf("unexpected Spark weekly bucket: %+v", spark)
	}
}

func TestNormalizeSummaryAcceptsWeeklyBucketWithoutTopLevelWindows(t *testing.T) {
	summary, err := normalizeSummary("app-server", rateLimitSnapshotRaw{}, map[string]rateLimitSnapshotRaw{
		"codex_bengalfox": {
			LimitName: stringPtr("Spark"),
			Primary:   rawWeeklyWindow(55),
		},
	}, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.WindowDataAvailable {
		t.Fatal("expected the Spark weekly bucket to make weekly data available")
	}
	if got := summary.RateLimitWindows["codex_bengalfox"].WeeklyWindow.UsedPercent; got != 55 {
		t.Fatalf("expected Spark weekly usage 55, got %d", got)
	}
}

func TestRateLimitWindowForModelUsesWeeklyDefaultAndSparkBuckets(t *testing.T) {
	summary := &Summary{RateLimitWindows: map[string]RateLimitWindow{
		"codex":           {WeeklyWindow: WindowSummary{UsedPercent: 12}},
		"codex_bengalfox": {LimitName: "Spark", WeeklyWindow: WindowSummary{UsedPercent: 55}},
	}}
	if id, window, ok := summary.RateLimitWindowForModel("gpt-5-codex"); !ok || id != "codex" || window.WeeklyWindow.UsedPercent != 12 {
		t.Fatalf("expected default weekly bucket, got id=%q window=%+v ok=%v", id, window, ok)
	}
	if id, window, ok := summary.RateLimitWindowForModel("gpt-5.3-codex-spark"); !ok || id != "codex_bengalfox" || window.WeeklyWindow.UsedPercent != 55 {
		t.Fatalf("expected Spark weekly bucket, got id=%q window=%+v ok=%v", id, window, ok)
	}
}

func TestRateLimitWindowForSparkDoesNotFallbackToDefault(t *testing.T) {
	summary := &Summary{RateLimitWindows: map[string]RateLimitWindow{
		"codex": {WeeklyWindow: WindowSummary{UsedPercent: 12}},
	}}
	if _, _, ok := summary.RateLimitWindowForModel("spark"); ok {
		t.Fatal("expected Spark routing to require a Spark bucket")
	}
}

func TestNormalizeSummaryRequiresAtLeastOneRawWindow(t *testing.T) {
	if _, err := normalizeSummary("oauth", rateLimitSnapshotRaw{}, nil, 0, nil, nil); err == nil {
		t.Fatal("expected missing raw windows to fail")
	}
}

func rawWeeklyWindow(used int) *rateLimitWindowRaw {
	return rawWindow(used, weeklyWindowMinutes)
}

func rawWindow(used, minutes int) *rateLimitWindowRaw {
	return &rateLimitWindowRaw{UsedPercent: used, WindowDurationMins: testIntPtr(minutes)}
}

func testIntPtr(value int) *int { return &value }

func stringPtr(value string) *string { return &value }
