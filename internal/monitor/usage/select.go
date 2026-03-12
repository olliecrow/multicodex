package usage

import (
	"context"
	"fmt"
	"time"
)

type SelectedAccount struct {
	Account                      MonitorAccount
	PrimaryUsedPercent           int
	SecondaryUsedPercent         int
	UsedPrimaryThresholdFallback bool
}

func NewSnapshotFetcherForAccounts(accounts []MonitorAccount) *Fetcher {
	f := &Fetcher{}
	f.replaceAccountFetchers(accounts)
	return f
}

func SelectBestAccount(ctx context.Context, accounts []MonitorAccount, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	f := NewSnapshotFetcherForAccounts(accounts)
	defer f.Close()
	return f.SelectAccount(ctx, maxPrimaryUsedPercent)
}

func (f *Fetcher) SelectAccount(ctx context.Context, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	if len(f.accounts) == 0 {
		return SelectedAccount{}, fmt.Errorf("no accounts available")
	}

	now := time.Now().UTC()
	f.refreshAccounts(now, false)

	results := f.fetchAccountsConcurrent(ctx, now)
	return selectBestAccountFromResults(results, maxPrimaryUsedPercent)
}

func selectBestAccountFromResults(results []accountFetchResult, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	bestEligible := -1
	bestOverall := -1

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		if bestOverall == -1 || result.account.SecondaryWindow.UsedPercent < results[bestOverall].account.SecondaryWindow.UsedPercent {
			bestOverall = i
		}

		if result.account.PrimaryWindow.UsedPercent >= maxPrimaryUsedPercent {
			continue
		}
		if bestEligible == -1 || result.account.SecondaryWindow.UsedPercent < results[bestEligible].account.SecondaryWindow.UsedPercent {
			bestEligible = i
		}
	}

	if bestEligible != -1 {
		chosen := results[bestEligible]
		return SelectedAccount{
			Account:              MonitorAccount{Label: chosen.account.Label, CodexHome: chosen.codexHome},
			PrimaryUsedPercent:   chosen.account.PrimaryWindow.UsedPercent,
			SecondaryUsedPercent: chosen.account.SecondaryWindow.UsedPercent,
		}, nil
	}
	if bestOverall != -1 {
		chosen := results[bestOverall]
		return SelectedAccount{
			Account:                      MonitorAccount{Label: chosen.account.Label, CodexHome: chosen.codexHome},
			PrimaryUsedPercent:           chosen.account.PrimaryWindow.UsedPercent,
			SecondaryUsedPercent:         chosen.account.SecondaryWindow.UsedPercent,
			UsedPrimaryThresholdFallback: true,
		}, nil
	}

	return SelectedAccount{}, fmt.Errorf("no accessible accounts")
}
