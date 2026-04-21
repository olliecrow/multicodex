package usage

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"time"
)

type SelectedAccount struct {
	Account              MonitorAccount
	PrimaryUsedPercent   int
	SecondaryUsedPercent int
}

var chooseRandomResultIndex = func(candidates []int) int {
	if len(candidates) == 0 {
		return -1
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return candidates[0]
	}
	return candidates[int(n.Int64())]
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

	results := f.fetchAccountsConcurrent(ctx, now, activeHomeSet{})
	return selectBestAccountFromResults(results, maxPrimaryUsedPercent)
}

func selectBestAccountFromResults(results []accountFetchResult, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	eligibleUnknownResetCandidates := []int{}
	accessibleCandidates := []int{}
	soonestEligibleResetSeconds := int64(0)
	soonestEligibleCandidates := []int{}

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		accessibleCandidates = append(accessibleCandidates, i)

		primaryUsedPercent := result.account.PrimaryWindow.UsedPercent
		if primaryUsedPercent >= maxPrimaryUsedPercent {
			continue
		}

		secondsUntilReset, ok := secondsUntilReset(result.account.SecondaryWindow)
		if !ok {
			eligibleUnknownResetCandidates = append(eligibleUnknownResetCandidates, i)
			continue
		}

		if len(soonestEligibleCandidates) == 0 || secondsUntilReset < soonestEligibleResetSeconds {
			soonestEligibleResetSeconds = secondsUntilReset
			soonestEligibleCandidates = []int{i}
			continue
		}
		if secondsUntilReset == soonestEligibleResetSeconds {
			soonestEligibleCandidates = append(soonestEligibleCandidates, i)
		}
	}

	if selected, ok := chooseSelectedAccount(results, soonestEligibleCandidates); ok {
		return selected, nil
	}
	if selected, ok := chooseSelectedAccount(results, eligibleUnknownResetCandidates); ok {
		return selected, nil
	}
	if selected, ok := chooseSelectedAccount(results, accessibleCandidates); ok {
		return selected, nil
	}

	return SelectedAccount{}, fmt.Errorf("no accessible accounts")
}

func chooseSelectedAccount(results []accountFetchResult, candidates []int) (SelectedAccount, bool) {
	chosenIndex := chooseRandomResultIndex(candidates)
	if chosenIndex == -1 {
		return SelectedAccount{}, false
	}

	chosen := results[chosenIndex]
	return SelectedAccount{
		Account:              MonitorAccount{Label: chosen.account.Label, CodexHome: chosen.codexHome},
		PrimaryUsedPercent:   chosen.account.PrimaryWindow.UsedPercent,
		SecondaryUsedPercent: chosen.account.SecondaryWindow.UsedPercent,
	}, true
}

func secondsUntilReset(win WindowSummary) (int64, bool) {
	if win.UsedPercent == unavailableUsedPercent {
		return 0, false
	}

	if win.SecondsUntilReset != nil {
		if *win.SecondsUntilReset < 0 {
			return 0, true
		}
		return *win.SecondsUntilReset, true
	}

	if win.ResetsAt == nil {
		return 0, false
	}

	seconds := int64(time.Until(*win.ResetsAt).Seconds())
	if seconds < 0 {
		return 0, true
	}
	return seconds, true
}
