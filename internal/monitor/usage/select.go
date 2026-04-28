package usage

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type SelectedAccount struct {
	Account              MonitorAccount
	PrimaryUsedPercent   int
	SecondaryUsedPercent int
}

type accountWindowCandidate struct {
	resultIndex          int
	primaryUsedPercent   int
	secondaryUsedPercent int
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
	return SelectBestAccountForModel(ctx, accounts, maxPrimaryUsedPercent, "")
}

func SelectBestAccountForModel(ctx context.Context, accounts []MonitorAccount, maxPrimaryUsedPercent int, model string) (SelectedAccount, error) {
	f := NewSnapshotFetcherForAccounts(accounts)
	defer f.Close()
	return f.SelectAccountForModel(ctx, maxPrimaryUsedPercent, model)
}

func (f *Fetcher) SelectAccount(ctx context.Context, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	return f.SelectAccountForModel(ctx, maxPrimaryUsedPercent, "")
}

func (f *Fetcher) SelectAccountForModel(ctx context.Context, maxPrimaryUsedPercent int, model string) (SelectedAccount, error) {
	if len(f.accounts) == 0 {
		return SelectedAccount{}, fmt.Errorf("no accounts available")
	}

	now := time.Now().UTC()
	f.refreshAccounts(now, false)

	results := f.fetchAccountsConcurrent(ctx, now, activeHomeSet{})
	return selectBestAccountFromResultsForModel(results, maxPrimaryUsedPercent, model)
}

func selectBestAccountFromResults(results []accountFetchResult, maxPrimaryUsedPercent int) (SelectedAccount, error) {
	return selectBestAccountFromResultsForModel(results, maxPrimaryUsedPercent, "")
}

func selectBestAccountFromResultsForModel(results []accountFetchResult, maxPrimaryUsedPercent int, model string) (SelectedAccount, error) {
	modelIsSpark := isSparkModel(model)
	eligibleUnknownResetCandidates := []accountWindowCandidate{}
	accessibleCandidates := []accountWindowCandidate{}
	soonestEligibleResetSeconds := int64(0)
	soonestEligibleCandidates := []accountWindowCandidate{}
	hadModelWindow := false

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		primaryWindow, secondaryWindow, hasModelWindow := selectWindowsForModel(result.account, model)
		if hasModelWindow {
			hadModelWindow = true
		}

		candidate := accountWindowCandidate{
			resultIndex:          i,
			primaryUsedPercent:   primaryWindow.UsedPercent,
			secondaryUsedPercent: secondaryWindow.UsedPercent,
		}
		accessibleCandidates = append(accessibleCandidates, candidate)
		if modelIsSpark && !hasModelWindow {
			continue
		}

		if primaryWindow.UsedPercent >= maxPrimaryUsedPercent {
			continue
		}
		if weeklyWindowIsKnownExhausted(secondaryWindow) {
			continue
		}

		secondsUntilReset, ok := secondsUntilReset(secondaryWindow)
		if !ok {
			eligibleUnknownResetCandidates = append(eligibleUnknownResetCandidates, candidate)
			continue
		}

		if len(soonestEligibleCandidates) == 0 || secondsUntilReset < soonestEligibleResetSeconds {
			soonestEligibleResetSeconds = secondsUntilReset
			soonestEligibleCandidates = []accountWindowCandidate{candidate}
			continue
		}
		if secondsUntilReset == soonestEligibleResetSeconds {
			soonestEligibleCandidates = append(soonestEligibleCandidates, candidate)
		}
	}

	if selected, ok := chooseSelectedAccount(results, soonestEligibleCandidates); ok {
		return selected, nil
	}
	if selected, ok := chooseSelectedAccount(results, eligibleUnknownResetCandidates); ok {
		return selected, nil
	}
	if modelIsSpark && !hadModelWindow {
		return SelectedAccount{}, fmt.Errorf("no model-specific rate-limit windows available for requested model %q", model)
	}
	if modelIsSpark && hadModelWindow {
		return SelectedAccount{}, fmt.Errorf("no model-eligible accounts available for requested model %q", model)
	}
	if selected, ok := chooseSelectedAccount(results, accessibleCandidates); ok {
		return selected, nil
	}

	return SelectedAccount{}, fmt.Errorf("no accessible accounts")
}

func selectWindowsForModel(account AccountSummary, model string) (WindowSummary, WindowSummary, bool) {
	model = strings.TrimSpace(model)
	if model != "" {
		if _, window, ok := account.RateLimitWindowForModel(model); ok {
			return window.PrimaryWindow, window.SecondaryWindow, true
		}
	}
	return account.PrimaryWindow, account.SecondaryWindow, false
}

func weeklyWindowIsKnownExhausted(win WindowSummary) bool {
	return win.UsedPercent != unavailableUsedPercent && win.UsedPercent >= 100
}

func chooseSelectedAccount(results []accountFetchResult, candidates []accountWindowCandidate) (SelectedAccount, bool) {
	candidateIndexes := make([]int, len(candidates))
	for i := range candidates {
		candidateIndexes[i] = i
	}
	chosenCandidateIndex := chooseRandomResultIndex(candidateIndexes)
	if chosenCandidateIndex == -1 {
		return SelectedAccount{}, false
	}

	chosen := candidates[chosenCandidateIndex]
	chosenResult := results[chosen.resultIndex]
	primaryUsedPercent := chosen.primaryUsedPercent
	secondaryUsedPercent := chosen.secondaryUsedPercent

	return SelectedAccount{
		Account:              MonitorAccount{Label: chosenResult.account.Label, CodexHome: chosenResult.codexHome},
		PrimaryUsedPercent:   primaryUsedPercent,
		SecondaryUsedPercent: secondaryUsedPercent,
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
