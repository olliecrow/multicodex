package usage

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	name   string
	out    *Summary
	err    error
	closed bool
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(context.Context) (*Summary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}
func (f *fakeSource) Close() error {
	f.closed = true
	return nil
}

type blockingSource struct {
	name string
}

func (b *blockingSource) Name() string { return b.name }
func (b *blockingSource) Fetch(ctx context.Context) (*Summary, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockingSource) Close() error { return nil }

func TestFetcherUsesPrimaryOnSuccess(t *testing.T) {
	primary := &fakeSource{name: "primary", out: &Summary{Source: "primary"}}
	fallback := &fakeSource{name: "fallback", out: &Summary{Source: "fallback"}}
	f := &Fetcher{primary: primary, fallback: fallback}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Source != "primary" {
		t.Fatalf("expected primary source, got %s", out.Source)
	}
}

func TestFetcherFallsBackWithWarning(t *testing.T) {
	primary := &fakeSource{name: "primary", err: errors.New("boom")}
	fallback := &fakeSource{name: "fallback", out: &Summary{Source: "fallback"}}
	f := &Fetcher{primary: primary, fallback: fallback}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Source != "fallback" {
		t.Fatalf("expected fallback source, got %s", out.Source)
	}
	if len(out.Warnings) == 0 {
		t.Fatalf("expected warning from primary failure")
	}
	if !strings.Contains(out.Warnings[0], "primary") {
		t.Fatalf("warning should mention primary failure")
	}
}

func TestFetchWithFallbackReservesTimeForFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := fetchWithFallback(ctx, &blockingSource{name: "primary"}, &fakeSource{name: "fallback", out: &Summary{Source: "fallback"}})
	if err != nil {
		t.Fatalf("expected fallback success, got error: %v", err)
	}
	if out.Source != "fallback" {
		t.Fatalf("expected fallback summary, got %q", out.Source)
	}
	if time.Since(start) >= 95*time.Millisecond {
		t.Fatalf("expected fallback to succeed before parent context expiry")
	}
	if len(out.Warnings) == 0 || !strings.Contains(out.Warnings[0], `primary source "primary" failed`) {
		t.Fatalf("expected primary failure warning, got %+v", out.Warnings)
	}
}

func TestFetcherFailsWhenBothSourcesFail(t *testing.T) {
	primary := &fakeSource{name: "primary", err: errors.New("p")}
	fallback := &fakeSource{name: "fallback", err: errors.New("f")}
	f := &Fetcher{primary: primary, fallback: fallback}

	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("expected error to include both sources: %v", err)
	}
}

func TestFetcherCloseClosesAllSources(t *testing.T) {
	primary := &fakeSource{name: "primary"}
	fallback := &fakeSource{name: "fallback"}
	f := &Fetcher{primary: primary, fallback: fallback}

	if err := f.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if !primary.closed || !fallback.closed {
		t.Fatalf("expected both sources to close")
	}
}

type fakeEstimator struct {
	values map[string]ObservedTokenEstimate
	errs   map[string]error
}

func (f fakeEstimator) Estimate(codexHome string, _ time.Time) (ObservedTokenEstimate, error) {
	if err, ok := f.errs[codexHome]; ok {
		return ObservedTokenEstimate{
			Status: observedTokensStatusUnavailable,
			Note:   err.Error(),
		}, err
	}
	v, ok := f.values[codexHome]
	if !ok {
		return ObservedTokenEstimate{
			Status: observedTokensStatusUnavailable,
			Note:   "missing estimate",
		}, errors.New("missing estimate")
	}
	return v, nil
}

func TestFetcherAggregatesMultiAccountObservedTokens(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/b")

	primaryA := &fakeSource{name: "primary-a", out: &Summary{
		Source:          "app-server",
		PlanType:        "pro",
		AccountEmail:    "a@example.com",
		PrimaryWindow:   WindowSummary{UsedPercent: 20},
		SecondaryWindow: WindowSummary{UsedPercent: 50},
	}}
	primaryB := &fakeSource{name: "primary-b", err: errors.New("boom")}
	fallbackB := &fakeSource{name: "fallback-b", out: &Summary{
		Source:          "oauth",
		PlanType:        "pro",
		AccountEmail:    "b@example.com",
		PrimaryWindow:   WindowSummary{UsedPercent: 60},
		SecondaryWindow: WindowSummary{UsedPercent: 70},
	}}

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  primaryA,
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  primaryB,
				fallback: fallbackB,
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Window5h:     ObservedTokenBreakdown{Total: 100},
					WindowWeekly: ObservedTokenBreakdown{Total: 200},
					Status:       observedTokensStatusEstimated,
				},
				"/b": {
					Window5h:     ObservedTokenBreakdown{Total: 30},
					WindowWeekly: ObservedTokenBreakdown{Total: 80},
					Status:       observedTokensStatusEstimated,
				},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TotalAccounts != 2 || out.SuccessfulAccounts != 2 {
		t.Fatalf("expected 2/2 account success, got %d/%d", out.SuccessfulAccounts, out.TotalAccounts)
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 130 {
		t.Fatalf("expected aggregated 5h observed total, got %+v", out.ObservedTokens5h)
	}
	if out.ObservedTokensWeekly == nil || *out.ObservedTokensWeekly != 280 {
		t.Fatalf("expected aggregated weekly observed total, got %+v", out.ObservedTokensWeekly)
	}
	if out.ObservedTokensStatus != observedTokensStatusEstimated {
		t.Fatalf("expected estimated observed status, got %q", out.ObservedTokensStatus)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("expected 2 account rows, got %d", len(out.Accounts))
	}
	if out.Accounts[1].Source != "oauth" {
		t.Fatalf("expected fallback source for account b, got %q", out.Accounts[1].Source)
	}
	if out.Accounts[0].ObservedTokens5h == nil || *out.Accounts[0].ObservedTokens5h != 100 {
		t.Fatalf("expected account a observed 5h total")
	}
	if out.Accounts[1].ObservedTokens5h == nil || *out.Accounts[1].ObservedTokens5h != 30 {
		t.Fatalf("expected account b observed 5h total")
	}
	if !out.WindowDataAvailable {
		t.Fatalf("expected active account window data to be available")
	}
	if out.SecondaryWindow.UsedPercent != 70 {
		t.Fatalf("expected top window summary from active account")
	}
	if out.WindowAccountLabel != "b" {
		t.Fatalf("expected active window account label b, got %q", out.WindowAccountLabel)
	}
}

func TestFetcherAllowsObservedOnlyWhenAllSourcesFail(t *testing.T) {
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", err: errors.New("p")},
				fallback: &fakeSource{name: "fallback-a", err: errors.New("f")},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Window5h:     ObservedTokenBreakdown{Total: 12},
					WindowWeekly: ObservedTokenBreakdown{Total: 99},
					Status:       observedTokensStatusEstimated,
				},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.SuccessfulAccounts != 0 {
		t.Fatalf("expected zero successful accounts")
	}
	if out.ObservedTokensStatus != observedTokensStatusEstimated {
		t.Fatalf("expected observed estimate status")
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 12 {
		t.Fatalf("expected observed-only totals at summary level")
	}
}

func TestFetcherMarksObservedPartialWhenSomeAccountsUnavailable(t *testing.T) {
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", out: &Summary{PrimaryWindow: WindowSummary{}, SecondaryWindow: WindowSummary{}}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &fakeSource{name: "primary-b", out: &Summary{PrimaryWindow: WindowSummary{}, SecondaryWindow: WindowSummary{}}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Window5h:     ObservedTokenBreakdown{Total: 10},
					WindowWeekly: ObservedTokenBreakdown{Total: 20},
					Status:       observedTokensStatusEstimated,
				},
			},
			errs: map[string]error{
				"/b": errors.New("missing logs"),
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ObservedTokensStatus != observedTokensStatusPartial {
		t.Fatalf("expected partial observed status, got %q", out.ObservedTokensStatus)
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 10 {
		t.Fatalf("expected partial observed 5h total from available accounts")
	}
}

func TestFetcherMarksObservedWarmingWhenUnavailableEstimateIsWarming(t *testing.T) {
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account: MonitorAccount{Label: "a", CodexHome: "/a"},
				primary: &fakeSource{name: "primary-a", out: &Summary{
					PrimaryWindow:   WindowSummary{UsedPercent: 10},
					SecondaryWindow: WindowSummary{UsedPercent: 20},
				}},
				fallback: &fakeSource{name: "fallback-a"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Status:  observedTokensStatusUnavailable,
					Warming: true,
					Note:    "warming token estimate",
				},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ObservedTokensStatus != observedTokensStatusUnavailable {
		t.Fatalf("expected unavailable observed status, got %q", out.ObservedTokensStatus)
	}
	if !out.ObservedTokensWarming {
		t.Fatalf("expected warming flag when unavailable estimate is warming")
	}
	if len(out.Accounts) != 1 || !out.Accounts[0].ObservedTokensWarming {
		t.Fatalf("expected per-account warming flag to be set")
	}
}

func TestFetcherSumsObservedTotalsAcrossHomesForSameIdentity(t *testing.T) {
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account: MonitorAccount{Label: "a", CodexHome: "/a"},
				primary: &fakeSource{name: "primary-a", out: &Summary{
					AccountEmail:    "same@example.com",
					PrimaryWindow:   WindowSummary{UsedPercent: 10},
					SecondaryWindow: WindowSummary{UsedPercent: 20},
				}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account: MonitorAccount{Label: "b", CodexHome: "/b"},
				primary: &fakeSource{name: "primary-b", out: &Summary{
					AccountEmail:    "same@example.com",
					PrimaryWindow:   WindowSummary{UsedPercent: 30},
					SecondaryWindow: WindowSummary{UsedPercent: 40},
				}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Window5h: ObservedTokenBreakdown{
						Total:       100,
						Input:       80,
						CachedInput: 60,
						Output:      20,
						HasSplit:    true,
					},
					WindowWeekly: ObservedTokenBreakdown{
						Total:           200,
						Input:           150,
						CachedInput:     110,
						Output:          50,
						ReasoningOutput: 10,
						HasSplit:        true,
					},
					Status: observedTokensStatusEstimated,
				},
				"/b": {
					Window5h: ObservedTokenBreakdown{
						Total:           150,
						Input:           120,
						CachedInput:     90,
						Output:          30,
						ReasoningOutput: 10,
						HasSplit:        true,
					},
					WindowWeekly: ObservedTokenBreakdown{
						Total:       180,
						Input:       140,
						CachedInput: 100,
						Output:      40,
						HasSplit:    true,
					},
					Status: observedTokensStatusEstimated,
				},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 250 {
		t.Fatalf("expected summed 5h total across same-account homes, got %+v", out.ObservedTokens5h)
	}
	if out.ObservedTokensWeekly == nil || *out.ObservedTokensWeekly != 380 {
		t.Fatalf("expected summed weekly total across same-account homes, got %+v", out.ObservedTokensWeekly)
	}
	if out.ObservedWindow5h == nil || out.ObservedWindow5h.Input != 200 || out.ObservedWindow5h.CachedInput != 150 || out.ObservedWindow5h.Output != 50 || out.ObservedWindow5h.ReasoningOutput != 10 {
		t.Fatalf("expected 5h breakdown to add across same-account homes, got %+v", out.ObservedWindow5h)
	}
	if out.ObservedWindowWeekly == nil || out.ObservedWindowWeekly.Input != 290 || out.ObservedWindowWeekly.CachedInput != 210 || out.ObservedWindowWeekly.Output != 90 || out.ObservedWindowWeekly.ReasoningOutput != 10 {
		t.Fatalf("expected weekly breakdown to add across same-account homes, got %+v", out.ObservedWindowWeekly)
	}
	if out.TotalAccounts != 1 || out.SuccessfulAccounts != 1 {
		t.Fatalf("expected deduped identity counts 1/1, got %d/%d", out.SuccessfulAccounts, out.TotalAccounts)
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("expected deduped account row count 1, got %d", len(out.Accounts))
	}
}

func TestReplaceAccountFetchersClosesRemovedHomes(t *testing.T) {
	oldPrimary := &fakeSource{name: "old-primary"}
	oldFallback := &fakeSource{name: "old-fallback"}
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "old", CodexHome: "/old"},
				primary:  oldPrimary,
				fallback: oldFallback,
			},
		},
	}

	f.replaceAccountFetchers([]MonitorAccount{
		{Label: "new", CodexHome: "/new"},
	})

	if !oldPrimary.closed || !oldFallback.closed {
		t.Fatalf("expected removed account sources to be closed")
	}
	if len(f.accounts) != 1 {
		t.Fatalf("expected one replacement account fetcher")
	}
	if f.accounts[0].account.Label != "new" {
		t.Fatalf("expected replacement account label")
	}
}

func TestRefreshAccountsReloadsAndReusesExistingHomes(t *testing.T) {
	callCount := 0
	f := &Fetcher{
		accountLoader: func() ([]MonitorAccount, string, error) {
			callCount++
			switch callCount {
			case 1:
				return []MonitorAccount{
					{Label: "alpha", CodexHome: "/alpha"},
				}, "", nil
			default:
				return []MonitorAccount{
					{Label: "alpha-renamed", CodexHome: "/alpha"},
					{Label: "beta", CodexHome: "/beta"},
				}, "", nil
			}
		},
		accountRefreshInterval: time.Minute,
	}

	start := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
	f.refreshAccounts(start, true)
	if len(f.accounts) != 1 {
		t.Fatalf("expected one initial account")
	}
	reusedPrimary := f.accounts[0].primary

	f.refreshAccounts(start.Add(2*time.Minute), false)
	if len(f.accounts) != 2 {
		t.Fatalf("expected second refresh to load two accounts")
	}

	var alpha accountFetcher
	for _, account := range f.accounts {
		if account.account.CodexHome == "/alpha" {
			alpha = account
			break
		}
	}
	if alpha.account.Label != "alpha-renamed" {
		t.Fatalf("expected refreshed label for reused home")
	}
	if alpha.primary != reusedPrimary {
		t.Fatalf("expected existing source to be reused for unchanged home")
	}
}

func TestFetcherAddsObservedTotalsAcrossHomesWhenDedupingByAccountID(t *testing.T) {
	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account: MonitorAccount{Label: "a", CodexHome: "/a"},
				primary: &fakeSource{name: "primary-a", out: &Summary{
					AccountID:       "same-account-id",
					PrimaryWindow:   WindowSummary{UsedPercent: 10},
					SecondaryWindow: WindowSummary{UsedPercent: 20},
				}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account: MonitorAccount{Label: "b", CodexHome: "/b"},
				primary: &fakeSource{name: "primary-b", out: &Summary{
					AccountID:       "same-account-id",
					PrimaryWindow:   WindowSummary{UsedPercent: 20},
					SecondaryWindow: WindowSummary{UsedPercent: 30},
				}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {
					Window5h:     ObservedTokenBreakdown{Total: 100},
					WindowWeekly: ObservedTokenBreakdown{Total: 200},
					Status:       observedTokensStatusEstimated,
				},
				"/b": {
					Window5h:     ObservedTokenBreakdown{Total: 150},
					WindowWeekly: ObservedTokenBreakdown{Total: 180},
					Status:       observedTokensStatusEstimated,
				},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 250 {
		t.Fatalf("expected totals to add across homes for one account id, got %+v", out.ObservedTokens5h)
	}
	if out.TotalAccounts != 1 || out.SuccessfulAccounts != 1 {
		t.Fatalf("expected deduped identity counts 1/1, got %d/%d", out.SuccessfulAccounts, out.TotalAccounts)
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("expected deduped account row count 1, got %d", len(out.Accounts))
	}
}

func TestFetcherKeepsUnverifiedAccountsDistinctByHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/a")

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account: MonitorAccount{Label: "a", CodexHome: "/a"},
				primary: &fakeSource{name: "primary-a", out: &Summary{
					PrimaryWindow:   WindowSummary{UsedPercent: 10},
					SecondaryWindow: WindowSummary{UsedPercent: 20},
				}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account: MonitorAccount{Label: "b", CodexHome: "/b"},
				primary: &fakeSource{name: "primary-b", out: &Summary{
					PrimaryWindow:   WindowSummary{UsedPercent: 30},
					SecondaryWindow: WindowSummary{UsedPercent: 40},
				}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {Window5h: ObservedTokenBreakdown{Total: 100}, WindowWeekly: ObservedTokenBreakdown{Total: 200}, Status: observedTokensStatusEstimated},
				"/b": {Window5h: ObservedTokenBreakdown{Total: 150}, WindowWeekly: ObservedTokenBreakdown{Total: 180}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TotalAccounts != 2 || out.SuccessfulAccounts != 2 {
		t.Fatalf("expected unverified accounts to remain distinct, got %d/%d", out.SuccessfulAccounts, out.TotalAccounts)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("expected two account rows for unverified homes, got %d", len(out.Accounts))
	}
	if out.ObservedTokens5h == nil || *out.ObservedTokens5h != 250 {
		t.Fatalf("expected summed unverified observed 5h total, got %+v", out.ObservedTokens5h)
	}
	if out.ObservedTokensWeekly == nil || *out.ObservedTokensWeekly != 380 {
		t.Fatalf("expected summed unverified observed weekly total, got %+v", out.ObservedTokensWeekly)
	}
}

func TestFetcherUsesActiveHomeIdentityForCurrentAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/b")

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", out: &Summary{AccountEmail: "a@example.com", PrimaryWindow: WindowSummary{UsedPercent: 10}, SecondaryWindow: WindowSummary{UsedPercent: 20}}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &fakeSource{name: "primary-b", out: &Summary{AccountEmail: "b@example.com", PrimaryWindow: WindowSummary{UsedPercent: 15}, SecondaryWindow: WindowSummary{UsedPercent: 19}}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				"/b": {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.AccountEmail != "b@example.com" {
		t.Fatalf("expected current account from active CODEX_HOME, got %q", out.AccountEmail)
	}
	if out.WindowAccountLabel != "b" {
		t.Fatalf("expected window account to follow active CODEX_HOME, got %q", out.WindowAccountLabel)
	}
	if out.PrimaryWindow.UsedPercent != 15 || out.SecondaryWindow.UsedPercent != 19 {
		t.Fatalf("expected window cards to reflect active account limits")
	}
}

func TestFetcherMarksWindowUnavailableWhenActiveFetchFails(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/b")

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", out: &Summary{AccountEmail: "a@example.com", PrimaryWindow: WindowSummary{UsedPercent: 10}, SecondaryWindow: WindowSummary{UsedPercent: 20}}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &fakeSource{name: "primary-b", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-b", err: errors.New("fallback boom")},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				"/b": {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.WindowDataAvailable {
		t.Fatalf("expected active window data to be unavailable")
	}
	if out.AccountEmail != "" {
		t.Fatalf("expected no current account identity when active account failed, got %q", out.AccountEmail)
	}
	if out.WindowAccountLabel != "b" {
		t.Fatalf("expected active account label to remain available when active account failed, got %q", out.WindowAccountLabel)
	}
	if !strings.Contains(strings.Join(out.Warnings, " | "), "window cards are unavailable") {
		t.Fatalf("expected warning about unavailable window cards")
	}
}

func TestFetcherUsesDefaultAuthSymlinkTargetAsActiveAlias(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")

	defaultHome := filepath.Join(tmp, ".codex")
	profileHome := filepath.Join(tmp, "multicodex", "profiles", "crowoy", "codex-home")
	if err := os.MkdirAll(defaultHome, 0o700); err != nil {
		t.Fatalf("mkdir default home: %v", err)
	}
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}
	if err := os.Symlink(filepath.Join(profileHome, "auth.json"), filepath.Join(defaultHome, "auth.json")); err != nil {
		t.Fatalf("symlink default auth: %v", err)
	}

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "default", CodexHome: defaultHome},
				primary:  &fakeSource{name: "primary-default", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-default", err: errors.New("fallback boom")},
			},
			{
				account:  MonitorAccount{Label: "crowoy", CodexHome: profileHome},
				primary:  &fakeSource{name: "primary-crowoy", out: &Summary{AccountEmail: "crowoy@example.com", PrimaryWindow: WindowSummary{UsedPercent: 15}, SecondaryWindow: WindowSummary{UsedPercent: 19}}},
				fallback: &fakeSource{name: "fallback-crowoy"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				defaultHome: {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				profileHome: {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.WindowDataAvailable {
		t.Fatalf("expected active window data to remain available via auth symlink target")
	}
	if out.AccountEmail != "crowoy@example.com" {
		t.Fatalf("expected active account email from auth-linked profile, got %q", out.AccountEmail)
	}
	if out.WindowAccountLabel != "crowoy" {
		t.Fatalf("expected active window label from auth-linked profile, got %q", out.WindowAccountLabel)
	}
	if out.PrimaryWindow.UsedPercent != 15 || out.SecondaryWindow.UsedPercent != 19 {
		t.Fatalf("expected active windows from auth-linked profile")
	}
}

func TestFetcherPrefersProfileLabelForActiveAliasWhenBothRowsSucceed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")

	defaultHome := filepath.Join(tmp, ".codex")
	profileHome := filepath.Join(tmp, "multicodex", "profiles", "crowoy", "codex-home")
	if err := os.MkdirAll(defaultHome, 0o700); err != nil {
		t.Fatalf("mkdir default home: %v", err)
	}
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}
	if err := os.Symlink(filepath.Join(profileHome, "auth.json"), filepath.Join(defaultHome, "auth.json")); err != nil {
		t.Fatalf("symlink default auth: %v", err)
	}

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "crowoy", CodexHome: profileHome},
				primary:  &fakeSource{name: "primary-crowoy", out: &Summary{AccountEmail: "crowoy@example.com", PrimaryWindow: WindowSummary{UsedPercent: 15}, SecondaryWindow: WindowSummary{UsedPercent: 19}}},
				fallback: &fakeSource{name: "fallback-crowoy"},
			},
			{
				account:  MonitorAccount{Label: "default", CodexHome: defaultHome},
				primary:  &fakeSource{name: "primary-default", out: &Summary{AccountEmail: "crowoy@example.com", PrimaryWindow: WindowSummary{UsedPercent: 88}, SecondaryWindow: WindowSummary{UsedPercent: 89}}},
				fallback: &fakeSource{name: "fallback-default"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				defaultHome: {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				profileHome: {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.WindowAccountLabel != "crowoy" {
		t.Fatalf("expected active alias to prefer real profile label, got %q", out.WindowAccountLabel)
	}
	if out.PrimaryWindow.UsedPercent != 15 || out.SecondaryWindow.UsedPercent != 19 {
		t.Fatalf("expected active windows to follow real profile row")
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("expected alias rows to deduplicate, got %d", len(out.Accounts))
	}
	if out.Accounts[0].Label != "crowoy" {
		t.Fatalf("expected deduplicated row to keep real profile label, got %q", out.Accounts[0].Label)
	}
}

func TestFetcherDeduplicatesFailedAliasHomesUsingAuthEmail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")

	defaultHome := filepath.Join(tmp, ".codex")
	profileHome := filepath.Join(tmp, "multicodex", "profiles", "crowoy", "codex-home")
	if err := os.MkdirAll(defaultHome, 0o700); err != nil {
		t.Fatalf("mkdir default home: %v", err)
	}
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte(`{"email":"crowoy@example.com","tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}
	if err := os.Symlink(filepath.Join(profileHome, "auth.json"), filepath.Join(defaultHome, "auth.json")); err != nil {
		t.Fatalf("symlink default auth: %v", err)
	}

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "crowoy", CodexHome: profileHome},
				primary:  &fakeSource{name: "primary-crowoy", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-crowoy", err: errors.New("fallback boom")},
			},
			{
				account:  MonitorAccount{Label: "default", CodexHome: defaultHome},
				primary:  &fakeSource{name: "primary-default", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-default", err: errors.New("fallback boom")},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				defaultHome: {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				profileHome: {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TotalAccounts != 1 {
		t.Fatalf("expected failed alias homes to deduplicate to one logical account, got %d", out.TotalAccounts)
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("expected one account row after deduplication, got %d", len(out.Accounts))
	}
	if out.Accounts[0].AccountEmail != "crowoy@example.com" {
		t.Fatalf("expected auth-derived email on failed deduped row, got %q", out.Accounts[0].AccountEmail)
	}
	if out.WindowAccountLabel != "crowoy" {
		t.Fatalf("expected active alias to prefer real profile label, got %q", out.WindowAccountLabel)
	}
	if out.Accounts[0].Label != "crowoy" {
		t.Fatalf("expected failed deduplicated row to keep real profile label, got %q", out.Accounts[0].Label)
	}
}

func TestFetcherDeduplicatesFailedAliasHomesUsingResolvedAuthFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "")

	defaultHome := filepath.Join(tmp, ".codex")
	profileHome := filepath.Join(tmp, "multicodex", "profiles", "crowoy", "codex-home")
	if err := os.MkdirAll(defaultHome, 0o700); err != nil {
		t.Fatalf("mkdir default home: %v", err)
	}
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatalf("mkdir profile home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write profile auth: %v", err)
	}
	if err := os.Symlink(filepath.Join(profileHome, "auth.json"), filepath.Join(defaultHome, "auth.json")); err != nil {
		t.Fatalf("symlink default auth: %v", err)
	}

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "crowoy", CodexHome: profileHome},
				primary:  &fakeSource{name: "primary-crowoy", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-crowoy", err: errors.New("fallback boom")},
			},
			{
				account:  MonitorAccount{Label: "default", CodexHome: defaultHome},
				primary:  &fakeSource{name: "primary-default", err: errors.New("boom")},
				fallback: &fakeSource{name: "fallback-default", err: errors.New("fallback boom")},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				defaultHome: {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				profileHome: {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TotalAccounts != 1 {
		t.Fatalf("expected resolved auth-file identity to deduplicate alias homes, got %d", out.TotalAccounts)
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("expected one account row after auth-file deduplication, got %d", len(out.Accounts))
	}
}

func TestFetcherPrioritizesActiveAccountWhenWorkerPoolIsFull(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/active")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &blockingSource{name: "primary-a"},
				fallback: &blockingSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &blockingSource{name: "primary-b"},
				fallback: &blockingSource{name: "fallback-b"},
			},
			{
				account:  MonitorAccount{Label: "c", CodexHome: "/c"},
				primary:  &blockingSource{name: "primary-c"},
				fallback: &blockingSource{name: "fallback-c"},
			},
			{
				account:  MonitorAccount{Label: "d", CodexHome: "/d"},
				primary:  &blockingSource{name: "primary-d"},
				fallback: &blockingSource{name: "fallback-d"},
			},
			{
				account:  MonitorAccount{Label: "active", CodexHome: "/active"},
				primary:  &fakeSource{name: "primary-active", out: &Summary{AccountEmail: "active@example.com", PrimaryWindow: WindowSummary{UsedPercent: 12}, SecondaryWindow: WindowSummary{UsedPercent: 18}}},
				fallback: &fakeSource{name: "fallback-active"},
			},
		},
	}

	out, err := f.Fetch(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.WindowDataAvailable {
		t.Fatalf("expected active window data to stay available despite queued inactive accounts")
	}
	if out.WindowAccountLabel != "active" {
		t.Fatalf("expected active account label, got %q", out.WindowAccountLabel)
	}
	if out.AccountEmail != "active@example.com" {
		t.Fatalf("expected active account email, got %q", out.AccountEmail)
	}
}

func TestFetcherUpdatesWindowCardsWhenActiveHomeSwitches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", out: &Summary{AccountEmail: "a@example.com", PrimaryWindow: WindowSummary{UsedPercent: 11}, SecondaryWindow: WindowSummary{UsedPercent: 12}}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &fakeSource{name: "primary-b", out: &Summary{AccountEmail: "b@example.com", PrimaryWindow: WindowSummary{UsedPercent: 65}, SecondaryWindow: WindowSummary{UsedPercent: 99}}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				"/b": {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	t.Setenv("CODEX_HOME", "/a")
	first, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected first fetch error: %v", err)
	}
	if first.AccountEmail != "a@example.com" || first.WindowAccountLabel != "a" {
		t.Fatalf("expected first fetch to follow account a, got email=%q label=%q", first.AccountEmail, first.WindowAccountLabel)
	}
	if first.SecondaryWindow.UsedPercent != 12 {
		t.Fatalf("expected first window values from account a")
	}

	t.Setenv("CODEX_HOME", "/b")
	second, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected second fetch error: %v", err)
	}
	if second.AccountEmail != "b@example.com" || second.WindowAccountLabel != "b" {
		t.Fatalf("expected second fetch to follow account b, got email=%q label=%q", second.AccountEmail, second.WindowAccountLabel)
	}
	if second.SecondaryWindow.UsedPercent != 99 {
		t.Fatalf("expected second window values from account b")
	}
}

func TestFetcherMarksWindowUnavailableWhenActiveHomeMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CODEX_HOME", "/missing")

	f := &Fetcher{
		accounts: []accountFetcher{
			{
				account:  MonitorAccount{Label: "a", CodexHome: "/a"},
				primary:  &fakeSource{name: "primary-a", out: &Summary{AccountEmail: "a@example.com", PrimaryWindow: WindowSummary{UsedPercent: 10}, SecondaryWindow: WindowSummary{UsedPercent: 20}}},
				fallback: &fakeSource{name: "fallback-a"},
			},
			{
				account:  MonitorAccount{Label: "b", CodexHome: "/b"},
				primary:  &fakeSource{name: "primary-b", out: &Summary{AccountEmail: "b@example.com", PrimaryWindow: WindowSummary{UsedPercent: 25}, SecondaryWindow: WindowSummary{UsedPercent: 70}}},
				fallback: &fakeSource{name: "fallback-b"},
			},
		},
		observed: fakeEstimator{
			values: map[string]ObservedTokenEstimate{
				"/a": {Window5h: ObservedTokenBreakdown{Total: 1}, WindowWeekly: ObservedTokenBreakdown{Total: 2}, Status: observedTokensStatusEstimated},
				"/b": {Window5h: ObservedTokenBreakdown{Total: 3}, WindowWeekly: ObservedTokenBreakdown{Total: 4}, Status: observedTokensStatusEstimated},
			},
		},
	}

	out, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.WindowDataAvailable {
		t.Fatalf("expected active window data to be unavailable when active home is missing")
	}
	if out.AccountEmail != "" {
		t.Fatalf("expected no account email when active home is missing, got %q", out.AccountEmail)
	}
	if out.WindowAccountLabel != "" {
		t.Fatalf("expected no window account label when active home is missing, got %q", out.WindowAccountLabel)
	}
}

func TestNormalizeHomeConvertsRelativeToAbsolute(t *testing.T) {
	tmp := t.TempDir()
	rel := filepath.Join(".", filepath.Base(tmp))
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve cwd: %v", err)
	}
	expected := filepath.Clean(filepath.Join(cwd, rel))
	got := normalizeHome(rel)
	if got != expected {
		t.Fatalf("expected normalized home %q, got %q", expected, got)
	}
}

func TestFetcherRandomizedSelectionAndCountInvariants(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	rng := rand.New(rand.NewSource(42))
	identityPool := []struct {
		email     string
		accountID string
		userID    string
	}{
		{email: "a@example.com", accountID: "acc-a", userID: "user-a"},
		{email: "b@example.com", accountID: "acc-b", userID: "user-b"},
		{email: "", accountID: "acc-c", userID: ""},
		{email: "", accountID: "", userID: "user-d"},
		{email: "", accountID: "", userID: ""},
	}

	for iter := 0; iter < 200; iter++ {
		accountCount := 2 + rng.Intn(5)
		homes := make([]string, accountCount)
		fetchers := make([]accountFetcher, 0, accountCount)
		observedValues := map[string]ObservedTokenEstimate{}
		summariesByIndex := make([]*Summary, accountCount)

		for i := 0; i < accountCount; i++ {
			home := fmt.Sprintf("/h-%d-%d", iter, i)
			if i > 0 && rng.Intn(4) == 0 {
				home = homes[rng.Intn(i)]
			}
			homes[i] = home

			id := identityPool[rng.Intn(len(identityPool))]
			primaryWindow := WindowSummary{UsedPercent: rng.Intn(101)}
			secondaryWindow := WindowSummary{UsedPercent: rng.Intn(101)}
			summary := &Summary{
				Source:          "app-server",
				PlanType:        "pro",
				AccountEmail:    id.email,
				AccountID:       id.accountID,
				UserID:          id.userID,
				PrimaryWindow:   primaryWindow,
				SecondaryWindow: secondaryWindow,
				FetchedAt:       time.Date(2026, 2, 27, 0, 0, i, 0, time.UTC),
			}

			fail := rng.Intn(5) == 0
			primary := &fakeSource{name: fmt.Sprintf("p-%d", i), out: summary}
			fallback := &fakeSource{name: fmt.Sprintf("f-%d", i)}
			if fail {
				primary.err = errors.New("boom")
				fallback.err = errors.New("fallback boom")
			} else {
				summariesByIndex[i] = summary
			}

			observedValues[home] = ObservedTokenEstimate{
				Status: observedTokensStatusEstimated,
				Window5h: ObservedTokenBreakdown{
					Total: int64(100 + rng.Intn(900)),
				},
				WindowWeekly: ObservedTokenBreakdown{
					Total: int64(1000 + rng.Intn(9000)),
				},
			}

			fetchers = append(fetchers, accountFetcher{
				account:  MonitorAccount{Label: fmt.Sprintf("a-%d", i), CodexHome: home},
				primary:  primary,
				fallback: fallback,
			})
		}

		activeHome := homes[rng.Intn(len(homes))]
		t.Setenv("CODEX_HOME", activeHome)

		f := &Fetcher{
			accounts: fetchers,
			observed: fakeEstimator{values: observedValues},
		}

		out, err := f.Fetch(context.Background())
		successCount := 0
		for _, summary := range summariesByIndex {
			if summary != nil {
				successCount++
			}
		}
		if successCount == 0 {
			if err != nil {
				t.Fatalf("iter %d: expected observed-only success, got error: %v", iter, err)
			}
		} else if err != nil {
			t.Fatalf("iter %d: unexpected fetch error: %v", iter, err)
		}

		totalIdentities := map[string]struct{}{}
		successfulIdentities := map[string]struct{}{}
		var activeSummary *Summary
		expectedObserved5h := int64(0)
		expectedObserved1w := int64(0)
		observedByIdentity := map[string]observedWindowPair{}

		for idx, account := range fetchers {
			home := account.account.CodexHome
			summary := summariesByIndex[idx]
			accountOut := AccountSummary{
				Label: account.account.Label,
			}
			if summary != nil {
				accountOut.AccountEmail = summary.AccountEmail
				accountOut.AccountID = summary.AccountID
				accountOut.UserID = summary.UserID
				if home == activeHome {
					activeSummary = summary
				}
			}
			key := accountIdentityOrHomeKey(accountOut, home)
			totalIdentities[key] = struct{}{}
			if summary != nil {
				successfulIdentities[key] = struct{}{}
			}
			observed := observedValues[home]
			prev := observedByIdentity[key]
			observedByIdentity[key] = addObservedPairs(prev, observedWindowPair{
				Window5h:     observed.Window5h,
				WindowWeekly: observed.WindowWeekly,
			})
		}
		for _, pair := range observedByIdentity {
			expectedObserved5h += pair.Window5h.Total
			expectedObserved1w += pair.WindowWeekly.Total
		}

		if out.TotalAccounts != len(totalIdentities) {
			t.Fatalf("iter %d: expected total identity count %d, got %d", iter, len(totalIdentities), out.TotalAccounts)
		}
		if out.SuccessfulAccounts != len(successfulIdentities) {
			t.Fatalf("iter %d: expected successful identity count %d, got %d", iter, len(successfulIdentities), out.SuccessfulAccounts)
		}
		if len(out.Accounts) != len(totalIdentities) {
			t.Fatalf("iter %d: expected account row count %d, got %d", iter, len(totalIdentities), len(out.Accounts))
		}
		if out.ObservedTokens5h == nil || *out.ObservedTokens5h != expectedObserved5h {
			t.Fatalf("iter %d: expected observed 5h %d, got %+v", iter, expectedObserved5h, out.ObservedTokens5h)
		}
		if out.ObservedTokensWeekly == nil || *out.ObservedTokensWeekly != expectedObserved1w {
			t.Fatalf("iter %d: expected observed weekly %d, got %+v", iter, expectedObserved1w, out.ObservedTokensWeekly)
		}

		if activeSummary != nil {
			if !out.WindowDataAvailable {
				t.Fatalf("iter %d: expected active window data to be available", iter)
			}
			if out.AccountEmail != activeSummary.AccountEmail {
				t.Fatalf("iter %d: expected active account email %q, got %q", iter, activeSummary.AccountEmail, out.AccountEmail)
			}
			if out.PrimaryWindow.UsedPercent != activeSummary.PrimaryWindow.UsedPercent ||
				out.SecondaryWindow.UsedPercent != activeSummary.SecondaryWindow.UsedPercent {
				t.Fatalf("iter %d: expected active window pair %d/%d, got %d/%d",
					iter,
					activeSummary.PrimaryWindow.UsedPercent,
					activeSummary.SecondaryWindow.UsedPercent,
					out.PrimaryWindow.UsedPercent,
					out.SecondaryWindow.UsedPercent,
				)
			}
		} else {
			if out.WindowDataAvailable {
				t.Fatalf("iter %d: expected window data to be unavailable without active summary", iter)
			}
		}
	}
}
