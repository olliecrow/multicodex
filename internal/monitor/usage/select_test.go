package usage

import (
	"errors"
	"testing"
)

func TestSelectBestAccountPrefersLowestWeeklyAmongEligiblePrimaryUsage(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account: AccountSummary{
				Label:           "alpha",
				PrimaryWindow:   WindowSummary{UsedPercent: 30},
				SecondaryWindow: WindowSummary{UsedPercent: 40},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/beta",
			account: AccountSummary{
				Label:           "beta",
				PrimaryWindow:   WindowSummary{UsedPercent: 59},
				SecondaryWindow: WindowSummary{UsedPercent: 20},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/gamma",
			account: AccountSummary{
				Label:           "gamma",
				PrimaryWindow:   WindowSummary{UsedPercent: 70},
				SecondaryWindow: WindowSummary{UsedPercent: 5},
			},
			snapshot: &Summary{},
		},
	}, 60)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "beta" {
		t.Fatalf("expected beta, got %q", selected.Account.Label)
	}
	if selected.UsedPrimaryThresholdFallback {
		t.Fatalf("did not expect threshold fallback")
	}
}

func TestSelectBestAccountFallsBackToLowestWeeklyWhenNoPrimaryEligible(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account: AccountSummary{
				Label:           "alpha",
				PrimaryWindow:   WindowSummary{UsedPercent: 60},
				SecondaryWindow: WindowSummary{UsedPercent: 40},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/beta",
			account: AccountSummary{
				Label:           "beta",
				PrimaryWindow:   WindowSummary{UsedPercent: 92},
				SecondaryWindow: WindowSummary{UsedPercent: 20},
			},
			snapshot: &Summary{},
		},
	}, 60)
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

func TestSelectBestAccountKeepsFirstResultOnWeeklyTie(t *testing.T) {
	selected, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account: AccountSummary{
				Label:           "alpha",
				PrimaryWindow:   WindowSummary{UsedPercent: 25},
				SecondaryWindow: WindowSummary{UsedPercent: 20},
			},
			snapshot: &Summary{},
		},
		{
			codexHome: "/beta",
			account: AccountSummary{
				Label:           "beta",
				PrimaryWindow:   WindowSummary{UsedPercent: 15},
				SecondaryWindow: WindowSummary{UsedPercent: 20},
			},
			snapshot: &Summary{},
		},
	}, 60)
	if err != nil {
		t.Fatalf("selectBestAccountFromResults: %v", err)
	}
	if selected.Account.Label != "alpha" {
		t.Fatalf("expected alpha to win tie, got %q", selected.Account.Label)
	}
}

func TestSelectBestAccountErrorsWhenNoAccountsAccessible(t *testing.T) {
	_, err := selectBestAccountFromResults([]accountFetchResult{
		{
			codexHome: "/alpha",
			account:   AccountSummary{Label: "alpha"},
			fetchErr:  errors.New("boom"),
		},
	}, 60)
	if err == nil {
		t.Fatalf("expected error when all accounts fail")
	}
}
