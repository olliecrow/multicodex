package usage

import (
	"errors"
	"testing"
	"time"
)

func TestSelectBestAccountPrefersSoonestNonEmptySafeBucket(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("after-72h", 10, 40, 80*time.Hour),
		testAccountFetchResult("within-72h", 20, 70, 36*time.Hour),
		testAccountFetchResult("within-24h", 30, 90, 12*time.Hour),
		testAccountFetchResult("unsafe", 80, 1, 2*time.Hour),
	}, 50)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "within-24h" {
		t.Fatalf("expected within-24h, got %q", selected.Account.Label)
	}
	if selected.UsedPrimaryThresholdFallback {
		t.Fatalf("did not expect threshold fallback")
	}
}

func TestSelectBestAccountUsesWithin72HourBucketWhenSoonerBucketIsEmpty(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("after-72h", 10, 40, 96*time.Hour),
		testAccountFetchResult("within-72h", 20, 70, 48*time.Hour),
	}, 50)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "within-72h" {
		t.Fatalf("expected within-72h, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountKeepsUnknownWeeklyResetAfterKnownBuckets(t *testing.T) {
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
	}, 50)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "known" {
		t.Fatalf("expected known, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountChoosesRandomWithinWinningBucket(t *testing.T) {
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
		testAccountFetchResult("beta", 20, 41, 18*time.Hour),
		testAccountFetchResult("gamma", 30, 42, 80*time.Hour),
	}, 50)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountFallsBackToLowestPrimaryWhenNoSafeAccounts(t *testing.T) {
	originalChooser := chooseRandomResultIndex
	chooseRandomResultIndex = func(candidates []int) int {
		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}
		return candidates[1]
	}
	defer func() { chooseRandomResultIndex = originalChooser }()

	selected, err := selectBestAccountFromResults([]accountFetchResult{
		testAccountFetchResult("alpha", 65, 40, 12*time.Hour),
		testAccountFetchResult("beta", 65, 20, 48*time.Hour),
		testAccountFetchResult("gamma", 70, 5, 6*time.Hour),
	}, 50)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
	if !selected.UsedPrimaryThresholdFallback {
		t.Fatalf("expected threshold fallback")
	}
}

func TestWeeklyResetBucketForWindowUsesInclusiveEdges(t *testing.T) {
	cases := []struct {
		name string
		win  WindowSummary
		want int
	}{
		{
			name: "within 24 hours",
			win:  WindowSummary{UsedPercent: 10, SecondsUntilReset: int64Ptr(weeklyResetWithin24HoursSeconds)},
			want: weeklyResetBucketWithin24Hours,
		},
		{
			name: "within 72 hours",
			win:  WindowSummary{UsedPercent: 10, SecondsUntilReset: int64Ptr(weeklyResetWithin72HoursSeconds)},
			want: weeklyResetBucketWithin72Hours,
		},
		{
			name: "after 72 hours",
			win:  WindowSummary{UsedPercent: 10, SecondsUntilReset: int64Ptr(weeklyResetWithin72HoursSeconds + 1)},
			want: weeklyResetBucketAfter72Hours,
		},
		{
			name: "unknown",
			win:  WindowSummary{UsedPercent: unavailableUsedPercent},
			want: weeklyResetBucketUnknown,
		},
		{
			name: "missing reset time",
			win:  WindowSummary{UsedPercent: 10},
			want: weeklyResetBucketUnknown,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := weeklyResetBucketForWindow(tc.win); got != tc.want {
				t.Fatalf("weeklyResetBucketForWindow() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelectBestAccountErrorsWhenNoAccountsAccessible(t *testing.T) {
	_, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account:   AccountSummary{Label: "alpha"},
			fetchErr:  errors.New("boom"),
		},
	}, 50)
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
