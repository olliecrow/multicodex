package usage

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"sort"
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
	selectionPriority    int
	secondsUntilReset    int64
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

func SelectBestAccountForModel(ctx context.Context, accounts []MonitorAccount, maxPrimaryUsedPercent int, model string) (SelectedAccount, error) {
	f := NewSnapshotFetcherForAccounts(accounts)
	defer f.Close()
	return f.SelectAccountForModel(ctx, maxPrimaryUsedPercent, model)
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

func selectBestAccountFromResultsForModel(results []accountFetchResult, maxPrimaryUsedPercent int, model string) (SelectedAccount, error) {
	modelIsSpark := isSparkModel(model)
	eligibleUnknownResetCandidates := []accountWindowCandidate{}
	eligibleResetCandidates := []accountWindowCandidate{}
	hadModelWindow := false
	hadUsableWindow := false

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		primaryWindow, secondaryWindow, hasModelWindow := selectWindowsForModel(result.account, model)
		if hasModelWindow {
			hadModelWindow = true
		}

		if modelIsSpark && !hasModelWindow {
			continue
		}
		if !usageWindowsAvailable(primaryWindow, secondaryWindow) {
			continue
		}
		hadUsableWindow = true
		if usageWindowIsKnownExhausted(primaryWindow) || usageWindowIsKnownExhausted(secondaryWindow) {
			continue
		}
		if primaryWindow.UsedPercent >= maxPrimaryUsedPercent {
			continue
		}
		if reserveCandidateBlockedByLowerPriorityAccount(results, result.selectionPriority, model) {
			continue
		}

		candidate := accountWindowCandidate{
			resultIndex:          i,
			selectionPriority:    result.selectionPriority,
			primaryUsedPercent:   primaryWindow.UsedPercent,
			secondaryUsedPercent: secondaryWindow.UsedPercent,
		}

		secondsUntilReset, ok := secondsUntilReset(secondaryWindow)
		if !ok {
			eligibleUnknownResetCandidates = append(eligibleUnknownResetCandidates, candidate)
			continue
		}
		candidate.secondsUntilReset = secondsUntilReset
		eligibleResetCandidates = append(eligibleResetCandidates, candidate)
	}

	if selected, ok := choosePrioritizedEligibleAccount(results, eligibleResetCandidates, eligibleUnknownResetCandidates); ok {
		return selected, nil
	}
	if modelIsSpark && !hadModelWindow {
		return SelectedAccount{}, fmt.Errorf("no model-specific rate-limit windows available for requested model %q", model)
	}
	if modelIsSpark && hadModelWindow {
		return SelectedAccount{}, fmt.Errorf("no model-eligible accounts available for requested model %q", model)
	}
	if hadUsableWindow {
		return SelectedAccount{}, fmt.Errorf("no safe accounts available below %d%% five-hour usage with weekly usage below 100%%", maxPrimaryUsedPercent)
	}
	return SelectedAccount{}, fmt.Errorf("no usable account usage windows available")
}

func choosePrioritizedEligibleAccount(results []accountFetchResult, resetCandidates, unknownResetCandidates []accountWindowCandidate) (SelectedAccount, bool) {
	for _, priority := range sortedCandidatePriorities(resetCandidates, unknownResetCandidates) {
		if selected, ok := chooseSelectedAccount(results, soonestResetCandidatesForPriority(resetCandidates, priority)); ok {
			return selected, true
		}
		if selected, ok := chooseSelectedAccount(results, candidatesWithPriority(unknownResetCandidates, priority)); ok {
			return selected, true
		}
	}
	return SelectedAccount{}, false
}

func sortedCandidatePriorities(candidateGroups ...[]accountWindowCandidate) []int {
	seen := map[int]struct{}{}
	for _, candidates := range candidateGroups {
		for _, candidate := range candidates {
			seen[candidate.selectionPriority] = struct{}{}
		}
	}
	priorities := make([]int, 0, len(seen))
	for priority := range seen {
		priorities = append(priorities, priority)
	}
	sort.Ints(priorities)
	return priorities
}

func soonestResetCandidatesForPriority(candidates []accountWindowCandidate, priority int) []accountWindowCandidate {
	out := []accountWindowCandidate{}
	soonest := int64(0)
	for _, candidate := range candidates {
		if candidate.selectionPriority != priority {
			continue
		}
		if len(out) == 0 || candidate.secondsUntilReset < soonest {
			soonest = candidate.secondsUntilReset
			out = []accountWindowCandidate{candidate}
			continue
		}
		if candidate.secondsUntilReset == soonest {
			out = append(out, candidate)
		}
	}
	return out
}

func candidatesWithPriority(candidates []accountWindowCandidate, priority int) []accountWindowCandidate {
	out := []accountWindowCandidate{}
	for _, candidate := range candidates {
		if candidate.selectionPriority == priority {
			out = append(out, candidate)
		}
	}
	return out
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

func usageWindowsAvailable(primary, secondary WindowSummary) bool {
	return primary.UsedPercent != unavailableUsedPercent && secondary.UsedPercent != unavailableUsedPercent
}

func usageWindowIsKnownExhausted(win WindowSummary) bool {
	return win.UsedPercent != unavailableUsedPercent && win.UsedPercent >= 100
}

func reserveCandidateBlockedByLowerPriorityAccount(results []accountFetchResult, priority int, model string) bool {
	hasLowerPriorityAccount := false
	hasLowerPriorityUsableWindow := false
	for _, result := range results {
		if result.selectionPriority >= priority {
			continue
		}
		hasLowerPriorityAccount = true
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}
		primaryWindow, secondaryWindow, hasModelWindow := selectWindowsForModel(result.account, model)
		if isSparkModel(model) && !hasModelWindow {
			continue
		}
		if !usageWindowsAvailable(primaryWindow, secondaryWindow) {
			continue
		}
		hasLowerPriorityUsableWindow = true
		if !usageWindowIsKnownExhausted(secondaryWindow) {
			return true
		}
	}
	return hasLowerPriorityAccount && !hasLowerPriorityUsableWindow
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
