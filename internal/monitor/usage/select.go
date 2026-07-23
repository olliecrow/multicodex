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
	Account           MonitorAccount
	WeeklyUsedPercent int
}

type accountWindowCandidate struct {
	resultIndex       int
	selectionPriority int
	secondsUntilReset int64
	weeklyUsedPercent int
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

func newSnapshotFetcherForAccounts(accounts []MonitorAccount) *Fetcher {
	f := &Fetcher{}
	f.replaceAccountFetchers(accounts)
	return f
}

func SelectBestAccountForModel(ctx context.Context, accounts []MonitorAccount, model string) (SelectedAccount, error) {
	f := newSnapshotFetcherForAccounts(accounts)
	defer f.Close()
	return f.SelectAccountForModel(ctx, model)
}

func (f *Fetcher) SelectAccountForModel(ctx context.Context, model string) (SelectedAccount, error) {
	if len(f.accounts) == 0 {
		return SelectedAccount{}, fmt.Errorf("no accounts available")
	}

	now := time.Now().UTC()
	f.refreshAccounts(now, false)
	results := f.fetchAccountsConcurrent(ctx, now, activeHomeSet{})
	return selectBestAccountFromResultsForModel(results, model)
}

func selectBestAccountFromResultsForModel(results []accountFetchResult, model string) (SelectedAccount, error) {
	modelIsSpark := isSparkModel(model)
	knownResetCandidates := []accountWindowCandidate{}
	unknownResetCandidates := []accountWindowCandidate{}
	hadModelWindow := false
	hadWeeklyWindow := false

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		weeklyWindow, hasModelWindow := selectWeeklyWindowForModel(result.account, model)
		if hasModelWindow {
			hadModelWindow = true
		}
		if modelIsSpark && !hasModelWindow {
			continue
		}
		if !usageWindowAvailable(weeklyWindow) {
			continue
		}
		hadWeeklyWindow = true
		if usageWindowIsKnownExhausted(weeklyWindow) {
			continue
		}
		if reserveCandidateBlockedByLowerPriorityAccount(results, result.selectionPriority, model) {
			continue
		}

		candidate := accountWindowCandidate{
			resultIndex:       i,
			selectionPriority: result.selectionPriority,
			weeklyUsedPercent: weeklyWindow.UsedPercent,
		}
		seconds, known := secondsUntilReset(weeklyWindow)
		if !known {
			unknownResetCandidates = append(unknownResetCandidates, candidate)
			continue
		}
		candidate.secondsUntilReset = seconds
		knownResetCandidates = append(knownResetCandidates, candidate)
	}

	if selected, ok := choosePrioritizedEligibleAccount(results, knownResetCandidates, unknownResetCandidates); ok {
		return selected, nil
	}
	if selected, ok := chooseReserveFallbackAccount(results, model); ok {
		return selected, nil
	}
	if modelIsSpark && !hadModelWindow {
		return SelectedAccount{}, fmt.Errorf("no model-specific weekly limit available for requested model %q", model)
	}
	if modelIsSpark {
		return SelectedAccount{}, fmt.Errorf("no model-eligible accounts available for requested model %q", model)
	}
	if hadWeeklyWindow {
		return SelectedAccount{}, fmt.Errorf("no accounts with remaining weekly usage")
	}
	return SelectedAccount{}, fmt.Errorf("no usable weekly account usage available")
}

func choosePrioritizedEligibleAccount(results []accountFetchResult, knownResetCandidates, unknownResetCandidates []accountWindowCandidate) (SelectedAccount, bool) {
	for _, priority := range sortedCandidatePriorities(knownResetCandidates, unknownResetCandidates) {
		if selected, ok := chooseSelectedAccount(results, soonestResetCandidatesForPriority(knownResetCandidates, priority)); ok {
			return selected, true
		}
		if selected, ok := chooseSelectedAccount(results, candidatesWithPriority(unknownResetCandidates, priority)); ok {
			return selected, true
		}
	}
	return SelectedAccount{}, false
}

func chooseReserveFallbackAccount(results []accountFetchResult, model string) (SelectedAccount, bool) {
	candidates := []accountWindowCandidate{}
	for i, result := range results {
		if result.selectionPriority <= 0 {
			continue
		}
		if reserveCandidateBlockedByLowerPriorityAccount(results, result.selectionPriority, model) {
			continue
		}
		weeklyUsedPercent := unavailableUsedPercent
		if result.fetchErr == nil && result.snapshot != nil {
			weeklyWindow, hasModelWindow := selectWeeklyWindowForModel(result.account, model)
			if !isSparkModel(model) || hasModelWindow {
				weeklyUsedPercent = weeklyWindow.UsedPercent
			}
		}
		candidates = append(candidates, accountWindowCandidate{
			resultIndex:       i,
			selectionPriority: result.selectionPriority,
			weeklyUsedPercent: weeklyUsedPercent,
		})
	}
	for _, priority := range sortedCandidatePriorities(candidates) {
		if selected, ok := chooseSelectedAccount(results, candidatesWithPriority(candidates, priority)); ok {
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
	var out []accountWindowCandidate
	var soonest int64
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
	var out []accountWindowCandidate
	for _, candidate := range candidates {
		if candidate.selectionPriority == priority {
			out = append(out, candidate)
		}
	}
	return out
}

func selectWeeklyWindowForModel(account AccountSummary, model string) (WindowSummary, bool) {
	model = strings.TrimSpace(model)
	if model != "" {
		if _, window, ok := account.RateLimitWindowForModel(model); ok {
			return window.WeeklyWindow, true
		}
	}
	return account.WeeklyWindow, false
}

func usageWindowAvailable(weekly WindowSummary) bool {
	return weekly.UsedPercent != unavailableUsedPercent
}

func usageWindowIsKnownExhausted(win WindowSummary) bool {
	return win.UsedPercent != unavailableUsedPercent && win.UsedPercent >= 100
}

func reserveCandidateBlockedByLowerPriorityAccount(results []accountFetchResult, priority int, model string) bool {
	for _, result := range results {
		if result.selectionPriority >= priority || result.fetchErr != nil || result.snapshot == nil {
			continue
		}
		weeklyWindow, hasModelWindow := selectWeeklyWindowForModel(result.account, model)
		if isSparkModel(model) && !hasModelWindow {
			continue
		}
		if usageWindowAvailable(weeklyWindow) && !usageWindowIsKnownExhausted(weeklyWindow) {
			return true
		}
	}
	return false
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
	return SelectedAccount{
		Account:           MonitorAccount{Label: chosenResult.account.Label, CodexHome: chosenResult.codexHome},
		WeeklyUsedPercent: chosen.weeklyUsedPercent,
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
