package usage

import (
	"errors"
	"testing"
	"time"
)

func TestSelectBestAccountPrefersSoonestWeeklyResetAmongEligibleAccounts(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("later-reset", 10, 40, 80*time.Hour),
		testAccountFetchResult("sooner-reset", 20, 70, 36*time.Hour),
		testAccountFetchResult("not-eligible", 80, 1, 12*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "sooner-reset" {
		t.Fatalf("expected sooner-reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountTreatsExactThresholdAsNotEligible(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("at-threshold", 40, 10, 1*time.Hour),
		testAccountFetchResult("eligible", 39, 90, 48*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "eligible" {
		t.Fatalf("expected eligible, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountSkipsKnownWeeklyExhaustedAccounts(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("weekly-exhausted-sooner-reset", 0, 100, 1*time.Hour),
		testAccountFetchResult("weekly-available-later-reset", 0, 73, 48*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "weekly-available-later-reset" {
		t.Fatalf("expected weekly-available-later-reset, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesKnownWeeklyResetBeforeUnknownWeeklyReset(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
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
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "known" {
		t.Fatalf("expected known, got %q", selected.Account.Label)
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

	selected, err := selectBestAccountFromResults([]accountFetchResult{
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
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountChoosesRandomAccessibleAccountWhenNoAccountIsEligible(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) == 0 {
			return -1
		}
		if len(candidates) != 3 {
			t.Fatalf("expected 3 candidates, got %d", len(candidates))
		}
		return candidates[2]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("alpha", 65, 40, 12*time.Hour),
		testAccountFetchResult("beta", 65, 20, 48*time.Hour),
		testAccountFetchResult("gamma", 70, 5, 6*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "gamma" {
		t.Fatalf("expected gamma, got %q", selected.Account.Label)
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

	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("alpha", 10, 40, 12*time.Hour),
		testAccountFetchResult("beta", 20, 41, 12*time.Hour),
		testAccountFetchResult("gamma", 30, 42, 80*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountUsesRandomAccessibleFallbackWhenOnlyInaccessibleResultsRemain(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) == 0 {
			return -1
		}
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}
		return candidates[0]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/broken",
			account:   AccountSummary{Label: "broken"},
			fetchErr:  errors.New("boom"),
		},
		testAccountFetchResult("working", 85, 10, 2*time.Hour),
	}, 40)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "working" {
		t.Fatalf("expected working, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountErrorsWhenNoAccountsAccessible(t *testing.T) {
	_, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account:   AccountSummary{Label: "alpha"},
			fetchErr:  errors.New("boom"),
		},
	}, 40)
	if err == nil {
		t.Fatalf("expected error when all accounts fail")
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
