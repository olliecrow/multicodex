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

func TestSelectBestAccountOrdersByPriorityThenWeeklyReset(t *testing.T) {
	results := []accountFetchResult{
		selectionResult("lower-priority", 100, 20, 60),
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

func TestSelectBestAccountDoesNotUseUnavailablePriorityFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile accountFetchResult
	}{
		{name: "exhausted", profile: selectionResult("profile", 0, 100, 60)},
		{name: "missing", profile: selectionResult("profile", 0, unavailableUsedPercent, -1)},
		{name: "fetch failed", profile: accountFetchResult{codexHome: "/profile", account: AccountSummary{Label: "profile"}, fetchErr: errors.New("failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := selectionResult("fallback", 100, unavailableUsedPercent, -1)
			if selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{tc.profile, fallback}, ""); err == nil {
				t.Fatalf("expected unavailable candidates to fail, got %+v", selected)
			}
		})
	}
}

func TestCollapseFetchedCandidatesFailsClosedWhenDuplicateSnapshotsDisagree(t *testing.T) {
	first := selectionResult("personal", 0, 70, 600)
	first.account.AccountID = "shared-account"
	first.account.AccountEmail = "person@example.com"
	second := selectionResult("default", 0, 10, 60)
	second.account.AccountEmail = "person@example.com"

	collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{first, second})
	if len(collapsed) != 0 {
		t.Fatalf("disagreeing duplicate added routing capacity: %+v", collapsed)
	}
}

func TestCollapseFetchedCandidatesChoosesPhysicalHomeAfterConsistentSnapshot(t *testing.T) {
	first := selectionResult("personal", 0, 70, 600)
	first.account.AccountID = "shared-account"
	first.account.AccountEmail = "person@example.com"
	second := selectionResult("default", 0, 70, 600)
	second.account.AccountEmail = "person@example.com"

	collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{first, second})
	if len(collapsed) != 1 || collapsed[0].account.Label != "personal" {
		t.Fatalf("expected the first stable physical home after consistency, got %+v", collapsed)
	}
}

func TestCollapseFetchedCandidatesUsesExactAbsoluteResetDespiteCountdownDrift(t *testing.T) {
	reset := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	first := selectionResult("first", 0, 70, 600)
	first.account.AccountID = "shared-account"
	first.account.WeeklyWindow.ResetsAt = &reset
	second := selectionResult("second", 0, 70, 593)
	second.account.AccountID = "shared-account"
	second.account.WeeklyWindow.ResetsAt = &reset

	collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{first, second})
	if len(collapsed) != 1 {
		t.Fatalf("same absolute reset did not collapse: %+v", collapsed)
	}
}

func TestCollapseFetchedCandidatesAcceptsOnlyBoundedRelativeResetDrift(t *testing.T) {
	for _, test := range []struct {
		name        string
		drift       int64
		wantMembers int
	}{
		{
			name:        "small concurrent fetch drift",
			drift:       relativeResetDriftToleranceSeconds,
			wantMembers: 1,
		},
		{
			name:        "material drift",
			drift:       relativeResetDriftToleranceSeconds + 1,
			wantMembers: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := selectionResult("first", 0, 70, 600)
			first.account.AccountID = "shared-account"
			second := selectionResult("second", 0, 70, 600-test.drift)
			second.account.AccountID = "shared-account"

			collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{first, second})
			if len(collapsed) != test.wantMembers {
				t.Fatalf("relative reset drift %d collapsed to %d member(s), want %d: %+v",
					test.drift, len(collapsed), test.wantMembers, collapsed)
			}
		})
	}
}

func TestCollapseFetchedCandidatesRejectsRelativeResetSpreadInEveryOrder(t *testing.T) {
	orders := [][]int64{
		{596, 600, 592},
		{600, 596, 592},
		{592, 596, 600},
	}
	for _, order := range orders {
		results := make([]accountFetchResult, 0, len(order))
		for index, seconds := range order {
			result := selectionResult(fmt.Sprintf("account-%d", index), 0, 70, seconds)
			result.account.AccountID = "shared-account"
			results = append(results, result)
		}
		if collapsed := collapseFetchedCandidatesByIdentity(results); len(collapsed) != 0 {
			t.Fatalf("relative reset spread %v collapsed despite exceeding tolerance: %+v", order, collapsed)
		}
	}
}

func TestCollapseFetchedCandidatesRejectsDifferentAbsoluteAndKnownUnknownResets(t *testing.T) {
	reset := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	laterReset := reset.Add(time.Second)
	for _, test := range []struct {
		name   string
		first  WindowSummary
		second WindowSummary
	}{
		{
			name: "different absolute resets despite equal countdowns",
			first: WindowSummary{
				UsedPercent:       70,
				ResetsAt:          &reset,
				SecondsUntilReset: int64Ptr(600),
			},
			second: WindowSummary{
				UsedPercent:       70,
				ResetsAt:          &laterReset,
				SecondsUntilReset: int64Ptr(600),
			},
		},
		{
			name:   "known and unknown reset",
			first:  weeklyWindow(70, 600),
			second: WindowSummary{UsedPercent: 70},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := selectionResult("first", 0, 70, 600)
			first.account.AccountID = "shared-account"
			first.account.WeeklyWindow = test.first
			second := selectionResult("second", 0, 70, 600)
			second.account.AccountID = "shared-account"
			second.account.WeeklyWindow = test.second

			collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{first, second})
			if len(collapsed) != 0 {
				t.Fatalf("different reset semantics added routing capacity: %+v", collapsed)
			}
		})
	}
}

func TestCollapseFetchedCandidatesComparesEveryRequestedBucketSemantic(t *testing.T) {
	unknownReset := WindowSummary{UsedPercent: 20}
	for _, test := range []struct {
		name   string
		model  string
		first  WindowSummary
		second WindowSummary
		spark  bool
	}{
		{
			name:   "weekly availability",
			first:  unavailableWindowSummary(),
			second: weeklyWindow(20, 60),
		},
		{
			name:   "exhaustion",
			first:  weeklyWindow(100, 60),
			second: weeklyWindow(99, 60),
		},
		{
			name:   "used percent",
			first:  weeklyWindow(20, 60),
			second: weeklyWindow(21, 60),
		},
		{
			name:   "reset semantics",
			first:  weeklyWindow(20, 60),
			second: unknownReset,
		},
		{
			name:   "required Spark bucket",
			model:  "spark",
			first:  weeklyWindow(20, 60),
			second: weeklyWindow(20, 60),
			spark:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := selectionResult("first", 0, 20, 60)
			first.account.AccountID = "shared-account"
			second := selectionResult("second", 0, 20, 60)
			second.account.AccountID = "shared-account"
			if test.spark {
				first.account.RateLimitWindows = map[string]RateLimitWindow{
					"spark": {LimitName: "Spark", WeeklyWindow: test.first},
				}
			} else {
				first.account.WeeklyWindow = test.first
				second.account.WeeklyWindow = test.second
			}

			collapsed := collapseFetchedCandidatesByIdentityForModel(
				[]accountFetchResult{first, second},
				test.model,
			)
			if len(collapsed) != 0 {
				t.Fatalf("disagreement added routing capacity: %+v", collapsed)
			}
		})
	}
}

func TestCollapseFetchedCandidatesExcludesMissingAndConflictedIdentityCapacity(t *testing.T) {
	strongOne := selectionResult("strong-one", 0, 10, 60)
	strongOne.account.AccountID = "account-one"
	strongOne.account.AccountEmail = "shared@example.com"
	strongTwo := selectionResult("strong-two", 0, 20, 120)
	strongTwo.account.AccountID = "account-two"
	strongTwo.account.AccountEmail = "shared@example.com"
	ambiguous := selectionResult("ambiguous", 0, 30, 180)
	ambiguous.account.AccountEmail = "shared@example.com"
	missing := selectionResult("missing", 0, 40, 240)
	missing.account.UserID = "user-only-must-not-route"

	collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{
		strongOne,
		strongTwo,
		ambiguous,
		missing,
	})
	if len(collapsed) != 2 {
		t.Fatalf("collapsed count: got %d want 2", len(collapsed))
	}
	if collapsed[0].account.Label != "strong-one" || collapsed[1].account.Label != "strong-two" {
		t.Fatalf("unexpected routing capacity: %+v", collapsed)
	}
}

func TestCollapseFetchedCandidatesUsesLaterStableSuccessWhenFirstDuplicateFailed(t *testing.T) {
	failed := accountFetchResult{
		account: AccountSummary{
			Label:        "alpha",
			AccountEmail: "person@example.com",
		},
		fetchErr: errors.New("failed"),
	}
	success := selectionResult("beta", 0, 30, 180)
	success.account.AccountEmail = "person@example.com"

	collapsed := collapseFetchedCandidatesByIdentity([]accountFetchResult{failed, success})
	if len(collapsed) != 1 || collapsed[0].account.Label != "beta" {
		t.Fatalf("expected the stable successful duplicate, got %+v", collapsed)
	}
}

func TestSelectAccountForModelUsesLogicalGroupForStandardAndSpark(t *testing.T) {
	shared := routingSummary("shared-account", "person@example.com", weeklyWindow(40, 60), weeklyWindow(80, 300))
	sharedDuplicate := routingSummary("", "person@example.com", weeklyWindow(40, 60), weeklyWindow(80, 300))
	independent := routingSummary("other-account", "other@example.com", weeklyWindow(10, 120), weeklyWindow(20, 90))

	for _, test := range []struct {
		name        string
		model       string
		wantLabel   string
		wantPercent int
	}{
		{name: "standard", wantLabel: "alpha", wantPercent: 40},
		{name: "Spark", model: "gpt-5.3-codex-spark", wantLabel: "beta", wantPercent: 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			fetcher := selectionFetcher(
				selectionAccountFetcher("alpha", "/alpha", shared, nil),
				selectionAccountFetcher("default", "/default", sharedDuplicate, nil),
				selectionAccountFetcher("beta", "/beta", independent, nil),
			)
			defer fetcher.Close()

			selected, err := fetcher.SelectAccountForModel(context.Background(), test.model)
			if err != nil {
				t.Fatal(err)
			}
			if selected.Account.Label != test.wantLabel || selected.WeeklyUsedPercent != test.wantPercent {
				t.Fatalf("selection: got %+v want label=%q used=%d", selected, test.wantLabel, test.wantPercent)
			}
		})
	}
}

func TestSelectAccountForModelFailsDisagreeingLogicalGroupClosed(t *testing.T) {
	first := routingSummary("shared-account", "person@example.com", weeklyWindow(10, 60), weeklyWindow(20, 60))
	second := routingSummary("shared-account", "person@example.com", weeklyWindow(90, 60), weeklyWindow(20, 60))
	fetcher := selectionFetcher(
		selectionAccountFetcher("alpha", "/alpha", first, nil),
		selectionAccountFetcher("default", "/default", second, nil),
	)
	defer fetcher.Close()

	if selected, err := fetcher.SelectAccountForModel(context.Background(), ""); err == nil {
		t.Fatalf("disagreeing logical group routed through %+v", selected)
	}
}

func TestSelectAccountForModelUsesLaterSuccessfulDuplicateAfterOfficialEmailRecovery(t *testing.T) {
	failedHome := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(failedHome, "auth.json"),
		[]byte(`{"email":"person@example.com"}`),
		0o600,
	); err != nil {
		t.Fatalf("write synthetic auth identity: %v", err)
	}
	success := routingSummary("shared-account", "person@example.com", weeklyWindow(30, 180), weeklyWindow(40, 240))
	fetcher := selectionFetcher(
		selectionAccountFetcher("alpha", failedHome, nil, errors.New("synthetic usage failure")),
		selectionAccountFetcher("beta", "/beta", success, nil),
	)
	defer fetcher.Close()

	selected, err := fetcher.SelectAccountForModel(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "beta" || selected.WeeklyUsedPercent != 30 {
		t.Fatalf("later successful duplicate selection: %+v", selected)
	}
}

func TestSelectAccountForModelExcludesAmbiguousEmailAndUserIDOnlyCapacity(t *testing.T) {
	strongOne := routingSummary("account-one", "shared@example.com", weeklyWindow(10, 60), weeklyWindow(20, 60))
	strongTwo := routingSummary("account-two", "shared@example.com", weeklyWindow(20, 120), weeklyWindow(30, 120))
	ambiguous := routingSummary("", "shared@example.com", weeklyWindow(1, 1), weeklyWindow(1, 1))
	userOnly := routingSummary("", "", weeklyWindow(1, 1), weeklyWindow(1, 1))
	userOnly.UserID = "user-only-must-not-route"
	fetcher := selectionFetcher(
		selectionAccountFetcher("strong-one", "/strong-one", strongOne, nil),
		selectionAccountFetcher("strong-two", "/strong-two", strongTwo, nil),
		selectionAccountFetcher("ambiguous", "/ambiguous", ambiguous, nil),
		selectionAccountFetcher("user-only", "/user-only", userOnly, nil),
	)
	defer fetcher.Close()

	selected, err := fetcher.SelectAccountForModel(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "strong-one" {
		t.Fatalf("unverified identity added routing capacity: %+v", selected)
	}
}

func TestSelectBestAccountHonorsGenericSelectionPriority(t *testing.T) {
	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("lower-priority", 100, 1, 60),
		selectionResult("higher-priority", 0, 99, -1),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "higher-priority" {
		t.Fatalf("expected generic priority to win before reset order, got %q", selected.Account.Label)
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
	if err == nil || !strings.HasPrefix(err.Error(), "no accounts with remaining weekly usage") {
		t.Fatalf("expected weekly exhaustion error, got %v", err)
	}
}

// The bare "weekly usage" wording reads as a week-long outage. It is only the
// name of the window we select on, and the real reset is often minutes away, so
// the message must always carry the horizon that refutes that reading.
func TestSelectBestAccountWeeklyExhaustionNamesResetHorizon(t *testing.T) {
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("exhausted-soon", 0, 100, 42*60),
	}, "")
	if err == nil {
		t.Fatal("expected an exhaustion error")
	}
	if !strings.Contains(err.Error(), "soonest reset in 42m") {
		t.Fatalf("exhaustion error must state when the window resets, got %q", err)
	}

	// Two accounts, different horizons: the caller needs the soonest, because
	// that is when work can resume.
	_, err = selectBestAccountFromResultsForModel([]accountFetchResult{
		selectionResult("later", 0, 100, 6*24*60*60),
		selectionResult("sooner", 0, 100, 15*60),
	}, "")
	if err == nil || !strings.Contains(err.Error(), "soonest reset in 15m") {
		t.Fatalf("expected the soonest of several resets, got %v", err)
	}
	if !strings.Contains(err.Error(), "2 exhausted") {
		t.Fatalf("expected the exhausted account count, got %v", err)
	}
}

func TestHumanizeResetDurationKeepsScaleUnmistakable(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		want    string
	}{
		{0, "under a minute"},
		{30, "under a minute"},
		{42 * 60, "42m"},
		{90 * 60, "1h30m"},
		{6 * 24 * 60 * 60, "6d00h"},
	} {
		if got := humanizeResetDuration(tc.seconds); got != tc.want {
			t.Fatalf("humanizeResetDuration(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
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

func TestSelectBestAccountAppliesSparkPolicyEquallyToDefaultAndManaged(t *testing.T) {
	defaultAccount := selectionResult("default", 0, 10, 600)
	defaultAccount.account.RateLimitWindows = map[string]RateLimitWindow{
		"codex_bengalfox": {LimitName: "Spark", WeeklyWindow: weeklyWindow(20, 60)},
	}
	managedAccount := selectionResult("managed", 0, 10, 600)
	managedAccount.account.RateLimitWindows = map[string]RateLimitWindow{
		"codex_bengalfox": {LimitName: "Spark", WeeklyWindow: weeklyWindow(30, 120)},
	}

	selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{managedAccount, defaultAccount}, "spark")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "default" {
		t.Fatalf("expected default account with sooner Spark reset, got %q", selected.Account.Label)
	}

	defaultAccount.account.RateLimitWindows = nil
	selected, err = selectBestAccountFromResultsForModel([]accountFetchResult{defaultAccount, managedAccount}, "spark")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Account.Label != "managed" {
		t.Fatalf("expected managed account when default lacks Spark usage, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountForSparkRequiresSparkWeeklyWindow(t *testing.T) {
	const privateModelArgument = "synthetic-private-spark-model"
	withoutSpark := selectionResult("standard", 0, 10, 60)
	_, err := selectBestAccountFromResultsForModel([]accountFetchResult{withoutSpark}, privateModelArgument)
	if err == nil || !strings.Contains(err.Error(), "model-specific weekly limit") {
		t.Fatalf("expected missing Spark weekly error, got %v", err)
	}
	if strings.Contains(err.Error(), privateModelArgument) {
		t.Fatalf("missing Spark weekly error repeated the model argument: %v", err)
	}

	withMissingSpark := selectionResult("missing", 0, 10, 60)
	withMissingSpark.account.RateLimitWindows = map[string]RateLimitWindow{
		"spark": {LimitName: "Spark", WeeklyWindow: unavailableWindowSummary()},
	}
	_, err = selectBestAccountFromResultsForModel([]accountFetchResult{withMissingSpark}, privateModelArgument)
	if err == nil || !strings.Contains(err.Error(), "model-eligible") {
		t.Fatalf("expected unusable Spark weekly error, got %v", err)
	}
	if strings.Contains(err.Error(), privateModelArgument) {
		t.Fatalf("model eligibility error repeated the model argument: %v", err)
	}
}

func TestSelectBestAccountForSparkDoesNotUseMissingModelFallback(t *testing.T) {
	profile := selectionResult("profile", 0, 20, 60)
	fallback := selectionResult("fallback", 100, 10, 60)
	if selected, err := selectBestAccountFromResultsForModel([]accountFetchResult{profile, fallback}, "spark"); err == nil {
		t.Fatalf("expected missing Spark buckets to fail, got %+v", selected)
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

func routingSummary(accountID, email string, standard, spark WindowSummary) *Summary {
	return &Summary{
		AccountID:    accountID,
		AccountEmail: email,
		WeeklyWindow: standard,
		RateLimitWindows: map[string]RateLimitWindow{
			"codex": {
				LimitID:      "codex",
				WeeklyWindow: standard,
			},
			"codex_bengalfox": {
				LimitID:      "codex_bengalfox",
				LimitName:    "Spark",
				WeeklyWindow: spark,
			},
		},
	}
}

func selectionAccountFetcher(label, home string, summary *Summary, fetchErr error) accountFetcher {
	return accountFetcher{
		account: MonitorAccount{Label: label, CodexHome: home},
		primary: &fakeSource{
			name: "synthetic-" + label,
			out:  summary,
			err:  fetchErr,
		},
	}
}

func selectionFetcher(accounts ...accountFetcher) *Fetcher {
	return &Fetcher{accounts: accounts}
}
