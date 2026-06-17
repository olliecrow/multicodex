package usage

import (
	"errors"
	"testing"
	"time"
)

func TestSelectBestAccountPrefersSoonestWeeklyResetAmongEligibleAccounts(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("later-reset", 10, 40, 80*time.Hour),
		testAccountFetchResult("sooner-reset", 20, 70, 36*time.Hour),
		testAccountFetchResult("not-eligible", 80, 1, 12*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "sooner-reset" {
		t.Fatalf("expected sooner-reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountTreatsExactGreenThresholdAsGreen(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("at-threshold", 40, 10, 1*time.Hour),
		testAccountFetchResult("eligible", 39, 90, 48*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "at-threshold" {
		t.Fatalf("expected at-threshold, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountSkipsKnownWeeklyExhaustedAccounts(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("weekly-exhausted-sooner-reset", 0, 100, 1*time.Hour),
		testAccountFetchResult("weekly-available-later-reset", 0, 73, 48*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "weekly-available-later-reset" {
		t.Fatalf("expected weekly-available-later-reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountPrefersGreenTierBeforeSoonerAmberOrRedReset(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("red-sooner-reset", 80, 10, 30*time.Minute),
		testAccountFetchResult("amber-sooner-reset", 50, 10, 1*time.Hour),
		testAccountFetchResult("green-later-reset", 40, 10, 48*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "green-later-reset" {
		t.Fatalf("expected green tier before sooner amber or red resets, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountPrefersAmberTierBeforeSoonerRedReset(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("red-sooner-reset", 80, 10, 30*time.Minute),
		testAccountFetchResult("amber-later-reset", 60, 10, 48*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "amber-later-reset" {
		t.Fatalf("expected amber tier before sooner red reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesKnownWeeklyResetBeforeUnknownWeeklyReset(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome: "/unknown",
			account: AccountSummary{
				Label:           "unknown",
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 30},
			},
			snapshot: &Summary{},
		},
		testAccountFetchResult("known", 20, 30, 96*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "known" {
		t.Fatalf("expected known, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesLowerPriorityBeforeReserveAccount(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome:         "/profile",
			selectionPriority: 0,
			account: AccountSummary{
				Label:           "profile",
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 30},
			},
			snapshot: &Summary{},
		},
		{
			codexHome:         "/default",
			selectionPriority: 100,
			account: AccountSummary{
				Label:           "default",
				PrimaryWindow:   WindowSummary{UsedPercent: 1},
				SecondaryWindow: WindowSummary{UsedPercent: 1, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			snapshot: &Summary{},
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "profile" {
		t.Fatalf("expected lower-priority profile account before reserve default account, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesReserveAccountWhenProfilesAreWeeklyExhausted(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome:         "/profile",
			selectionPriority: 0,
			account: AccountSummary{
				Label:           "profile",
				PrimaryWindow:   WindowSummary{UsedPercent: 80},
				SecondaryWindow: WindowSummary{UsedPercent: 100, SecondsUntilReset: testInt64Ptr(10 * 60)},
			},
			snapshot: &Summary{},
		},
		{
			codexHome:         "/default",
			selectionPriority: 100,
			account: AccountSummary{
				Label:           "default",
				PrimaryWindow:   WindowSummary{UsedPercent: 1},
				SecondaryWindow: WindowSummary{UsedPercent: 1, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			snapshot: &Summary{},
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "default" {
		t.Fatalf("expected reserve default account after profile accounts are weekly exhausted, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesReserveFallbackWhenOnlyReserveHasNoUsageLeft(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome:         "/profile",
			selectionPriority: 0,
			account: AccountSummary{
				Label:           "profile",
				PrimaryWindow:   WindowSummary{UsedPercent: 100},
				SecondaryWindow: WindowSummary{UsedPercent: 100, SecondsUntilReset: testInt64Ptr(10 * 60)},
			},
			snapshot: &Summary{},
		},
		{
			codexHome:         "/default",
			selectionPriority: 100,
			account: AccountSummary{
				Label:           "default",
				PrimaryWindow:   WindowSummary{UsedPercent: 100},
				SecondaryWindow: WindowSummary{UsedPercent: 100, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			snapshot: &Summary{},
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "default" {
		t.Fatalf("expected reserve fallback account, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesReserveFallbackWhenReserveUsageUnavailable(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome:         "/profile",
			selectionPriority: 0,
			account: AccountSummary{
				Label:           "profile",
				PrimaryWindow:   WindowSummary{UsedPercent: 100},
				SecondaryWindow: WindowSummary{UsedPercent: 20, SecondsUntilReset: testInt64Ptr(10 * 60)},
			},
			snapshot: &Summary{},
		},
		{
			codexHome:         "/default",
			selectionPriority: 100,
			account:           AccountSummary{Label: "default"},
			fetchErr:          errors.New("usage unavailable"),
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "default" {
		t.Fatalf("expected reserve fallback account with unavailable usage, got %q", selected.Account.Label)
	}
	if selected.PrimaryUsedPercent != unavailableUsedPercent || selected.SecondaryUsedPercent != unavailableUsedPercent {
		t.Fatalf("expected unavailable reserve usage metadata, got %d/%d", selected.PrimaryUsedPercent, selected.SecondaryUsedPercent)
	}
}

func TestSelectBestAccountUsesRedProfileBeforeReserveAccountWhenProfileHasUsageLeft(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome:         "/profile",
			selectionPriority: 0,
			account: AccountSummary{
				Label:           "profile",
				PrimaryWindow:   WindowSummary{UsedPercent: 80},
				SecondaryWindow: WindowSummary{UsedPercent: 30, SecondsUntilReset: testInt64Ptr(10 * 60)},
			},
			snapshot: &Summary{},
		},
		{
			codexHome:         "/default",
			selectionPriority: 100,
			account: AccountSummary{
				Label:           "default",
				PrimaryWindow:   WindowSummary{UsedPercent: 1},
				SecondaryWindow: WindowSummary{UsedPercent: 1, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			snapshot: &Summary{},
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "profile" {
		t.Fatalf("expected red profile with usage left before reserve account, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountNeverUsesWeeklyExhaustedAccounts(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("weekly-exhausted", 8, 100, 1*time.Hour),
	}, 40, "")
	if err == nil {
		t.Fatal("expected weekly-exhausted account to be rejected")
	}
}

func TestSelectBestAccountNeverUsesPrimaryExhaustedAccounts(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("primary-exhausted", 100, 10, 1*time.Hour),
	}, 40, "")
	if err == nil {
		t.Fatal("expected primary-exhausted account to be rejected")
	}
}

func TestSelectBestAccountUsesWeeklyAvailableRedProfile(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) == 0 {
			return -1
		}
		if len(candidates) != 1 {
			t.Fatalf("expected only the red account with usage left to remain, got %d candidates", len(candidates))
		}
		return candidates[0]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("weekly-exhausted", 8, 100, 1*time.Hour),
		testAccountFetchResult("red-but-usable", 66, 66, 48*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "red-but-usable" {
		t.Fatalf("expected red-but-usable, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountChoosesRandomEligibleAccountWhenAllEligibleWeeklyResetsAreUnknown(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) == 0 {
			return -1
		}
		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}
		return candidates[1]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome: "/alpha",
			account: AccountSummary{
				Label:           "alpha",
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 30},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/beta",
			account: AccountSummary{
				Label:           "beta",
				PrimaryWindow:   WindowSummary{UsedPercent: 20},
				SecondaryWindow: WindowSummary{UsedPercent: 31},
			},
			snapshot: &Summary{},
		},
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesRedTierWhenNoGreenOrAmberAccountIsUsable(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("alpha", 65, 40, 12*time.Hour),
		testAccountFetchResult("beta", 65, 20, 48*time.Hour),
		testAccountFetchResult("gamma", 70, 5, 6*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "gamma" {
		t.Fatalf("expected red account with soonest weekly reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountChoosesRandomAmongMatchingSoonestEligibleAccounts(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}
		return candidates[1]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResult("alpha", 10, 40, 12*time.Hour),
		testAccountFetchResult("beta", 20, 41, 12*time.Hour),
		testAccountFetchResult("gamma", 30, 42, 80*time.Hour),
	}, 40, "")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountErrorsWhenOnlyUnsafeResultsRemain(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome: "/broken",
			account:   AccountSummary{Label: "broken"},
			fetchErr:  errors.New("boom"),
		},
		testAccountFetchResult("primary-exhausted", 100, 10, 2*time.Hour),
	}, 40, "")
	if err == nil {
		t.Fatal("expected error when remaining account is exhausted")
	}
}

func TestSelectBestAccountErrorsWhenNoAccountsAccessible(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome: "/alpha",
			account:   AccountSummary{Label: "alpha"},
			fetchErr:  errors.New("boom"),
		},
	}, 40, "")
	if err == nil {
		t.Fatalf("expected error when all accounts fail")
	}
}

func TestSelectBestAccountForModelUsesSparkWindowForModelSelection(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	calls := 0
	chooseRandomResultIndex = func(candidates []int) int {
		calls++
		if len(candidates) == 0 {
			if calls > 1 {
				t.Fatalf("unexpected second empty candidate set")
			}
			return -1
		}
		if len(candidates) != 1 {
			t.Fatalf("expected spark-specific eligible candidate set to have 1 candidate, got %d", len(candidates))
		}
		return candidates[0]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	results := []accountFetchResult{
		testAccountFetchResultWithRateLimits("alpha", 10, 10, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 90},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 15},
				SecondaryWindow: WindowSummary{UsedPercent: 60, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
		}),
		testAccountFetchResultWithRateLimits("beta", 20, 20, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 20},
				SecondaryWindow: WindowSummary{UsedPercent: unavailableUsedPercent},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 45},
				SecondaryWindow: WindowSummary{UsedPercent: 40, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
		}),
	}
	p, s, _ := selectWindowsForModel(results[0].account, "gpt-5-codex-spark")
	if p.UsedPercent != 15 || s.UsedPercent != 60 {
		t.Fatalf("expected alpha spark windows to be {15, 60}, got {%d,%d}", p.UsedPercent, s.UsedPercent)
	}
	p, s, _ = selectWindowsForModel(results[1].account, "gpt-5-codex-spark")
	if p.UsedPercent != 45 || s.UsedPercent != 40 {
		t.Fatalf("expected beta spark windows to be {45, 40}, got {%d,%d}", p.UsedPercent, s.UsedPercent)
	}

	selected, err := selectBestAccountFromResultsForModel(results, 40, "gpt-5-codex-spark")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "alpha" {
		t.Fatalf("expected alpha (lower spark primary usage), got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountForModelFallsBackToDefaultWindowWhenModelMissing(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResultWithRateLimits("alpha", 50, 10, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 50},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 90},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(30 * 60)},
			},
		}),
		testAccountFetchResultWithRateLimits("beta", 10, 20, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(40 * 60)},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 80},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(40 * 60)},
			},
		}),
	}, 40, "gpt-4")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta (lower default primary usage), got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountForModelErrorsWhenSparkBucketMissing(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		{
			codexHome: "/alpha",
			account: AccountSummary{
				Label:           "alpha",
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 100},
				RateLimitWindows: map[string]RateLimitWindow{
					"codex_other": {
						PrimaryWindow:   WindowSummary{UsedPercent: 99},
						SecondaryWindow: WindowSummary{UsedPercent: unavailableUsedPercent},
					},
				},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/beta",
			account: AccountSummary{
				Label:           "beta",
				PrimaryWindow:   WindowSummary{UsedPercent: 5},
				SecondaryWindow: WindowSummary{UsedPercent: 10},
				RateLimitWindows: map[string]RateLimitWindow{
					"codex_other": {
						PrimaryWindow:   WindowSummary{UsedPercent: 80},
						SecondaryWindow: WindowSummary{UsedPercent: unavailableUsedPercent},
					},
				},
			},
			snapshot: &Summary{},
		},
	}, 40, "gpt-5-codex-spark")
	if err == nil {
		t.Fatalf("expected no model-specific window when requested model uses spark, got nil")
	}
}

func TestSelectBestAccountForModelUsesAmberSparkBucketWhenGreenIsExhausted(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		testAccountFetchResultWithRateLimits("alpha", 10, 10, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(18 * 60 * 60)},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 100},
				SecondaryWindow: WindowSummary{UsedPercent: 60, SecondsUntilReset: testInt64Ptr(6 * 24 * 60 * 60)},
			},
		}),
		testAccountFetchResultWithRateLimits("beta", 10, 10, map[string]RateLimitWindow{
			"codex": {
				PrimaryWindow:   WindowSummary{UsedPercent: 10},
				SecondaryWindow: WindowSummary{UsedPercent: 10, SecondsUntilReset: testInt64Ptr(18 * 60 * 60)},
			},
			"codex_bengalfox": {
				PrimaryWindow:   WindowSummary{UsedPercent: 55},
				SecondaryWindow: WindowSummary{UsedPercent: 40, SecondsUntilReset: testInt64Ptr(6 * 24 * 60 * 60)},
			},
		}),
	}, 40, "gpt-5-codex-spark")
	if err != nil {
		t.Fatalf("selectBestAccountFromResultsForModel: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta amber spark bucket, got %q", selected.Account.Label)
	}
}

func testAccountFetchResult(label string, primaryUsed, secondaryUsed int, weeklyResetIn time.Duration) accountFetchResult {
	account := AccountSummary{
		Label:         label,
		PrimaryWindow: WindowSummary{UsedPercent: primaryUsed},
		SecondaryWindow: WindowSummary{
			UsedPercent: secondaryUsed,
		},
	}
	if secondaryUsed != unavailableUsedPercent {
		seconds := int64(weeklyResetIn.Seconds())
		account.SecondaryWindow.SecondsUntilReset = &seconds
	}

	return accountFetchResult{
		codexHome: "/" + label,
		account:   account,
		snapshot:  &Summary{},
	}
}

func testAccountFetchResultWithRateLimits(label string, fallbackPrimaryUsed, fallbackSecondaryUsed int, windows map[string]RateLimitWindow) accountFetchResult {
	account := AccountSummary{
		Label:            label,
		PrimaryWindow:    WindowSummary{UsedPercent: fallbackPrimaryUsed},
		SecondaryWindow:  WindowSummary{UsedPercent: fallbackSecondaryUsed},
		RateLimitWindows: windows,
	}
	if fallbackSecondaryUsed != unavailableUsedPercent {
		seconds := int64(24 * time.Hour.Seconds())
		account.SecondaryWindow.SecondsUntilReset = &seconds
	}
	for limitID, window := range windows {
		if window.SecondaryWindow.UsedPercent != unavailableUsedPercent && window.SecondaryWindow.SecondsUntilReset == nil {
			seconds := int64(30 * 60)
			window.SecondaryWindow.SecondsUntilReset = &seconds
			windows[limitID] = window
		}
	}
	return accountFetchResult{
		codexHome: "/" + label,
		account:   account,
		snapshot:  &Summary{},
	}
}

func testInt64Ptr(v int64) *int64 {
	return &v
}
