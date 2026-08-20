package usage

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
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
	results = collapseFetchedCandidatesByIdentityForModel(results, model)
	return selectBestAccountFromResultsForModel(results, model)
}

func collapseFetchedCandidatesByIdentity(results []accountFetchResult) []accountFetchResult {
	return collapseFetchedCandidatesByIdentityForModel(results, "")
}

func collapseFetchedCandidatesByIdentityForModel(results []accountFetchResult, model string) []accountFetchResult {
	records := make([]CodexIdentityRecord, len(results))
	for index, result := range results {
		records[index] = CodexIdentityRecord{
			AccountID:    result.account.AccountID,
			AccountEmail: result.account.AccountEmail,
		}
	}

	groups := ReconcileCodexIdentities(records)
	collapsed := make([]accountFetchResult, 0, len(groups))
	for _, group := range groups {
		if !group.Resolved {
			continue
		}
		if !successfulDuplicateSnapshotsAgreeForModel(results, group.MemberIndexes, model) {
			continue
		}
		representativeIndex := deterministicSuccessfulResultIndex(results, group.MemberIndexes)
		if representativeIndex < 0 {
			continue
		}
		collapsed = append(collapsed, results[representativeIndex])
	}
	return collapsed
}

type weeklyEligibilitySnapshot struct {
	requiredBucketAvailable bool
	weeklyAvailable         bool
	exhausted               bool
	usedPercent             int
	reset                   weeklyResetSemantics
}

type weeklyResetSemantics struct {
	kind       uint8
	seconds    int64
	resetNanos int64
}

// relativeResetDriftToleranceSeconds bounds only countdown skew introduced by
// concurrent provider fetches. Exact provider reset timestamps and every other
// eligibility field still have to agree exactly.
const relativeResetDriftToleranceSeconds int64 = 5

func successfulDuplicateSnapshotsAgreeForModel(results []accountFetchResult, indexes []int, model string) bool {
	var expected weeklyEligibilitySnapshot
	var relativeResetMin int64
	var relativeResetMax int64
	relativeResetSeen := false
	successCount := 0
	for _, index := range indexes {
		if index < 0 || index >= len(results) {
			continue
		}
		result := results[index]
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}
		current := weeklyEligibilityForModel(result.account, model)
		if successCount == 0 {
			expected = current
		} else if !weeklyEligibilitySnapshotsAgree(expected, current) {
			return false
		}
		if current.reset.kind == 1 {
			if !relativeResetSeen {
				relativeResetMin = current.reset.seconds
				relativeResetMax = current.reset.seconds
				relativeResetSeen = true
			} else {
				if current.reset.seconds < relativeResetMin {
					relativeResetMin = current.reset.seconds
				}
				if current.reset.seconds > relativeResetMax {
					relativeResetMax = current.reset.seconds
				}
			}
		}
		successCount++
	}
	return successCount > 0 &&
		(!relativeResetSeen ||
			relativeResetMax-relativeResetMin <= relativeResetDriftToleranceSeconds)
}

func weeklyEligibilityForModel(account AccountSummary, model string) weeklyEligibilitySnapshot {
	weekly, hasModelWindow := selectWeeklyWindowForModel(account, model)
	requiredBucketAvailable := !isSparkModel(model) || hasModelWindow
	if !requiredBucketAvailable {
		return weeklyEligibilitySnapshot{}
	}
	return weeklyEligibilitySnapshot{
		requiredBucketAvailable: true,
		weeklyAvailable:         usageWindowAvailable(weekly),
		exhausted:               usageWindowIsKnownExhausted(weekly),
		usedPercent:             weekly.UsedPercent,
		reset:                   normalizedWeeklyResetSemantics(weekly),
	}
}

func weeklyEligibilitySnapshotsAgree(first, second weeklyEligibilitySnapshot) bool {
	return first.requiredBucketAvailable == second.requiredBucketAvailable &&
		first.weeklyAvailable == second.weeklyAvailable &&
		first.exhausted == second.exhausted &&
		first.usedPercent == second.usedPercent &&
		weeklyResetSemanticsAgree(first.reset, second.reset)
}

func normalizedWeeklyResetSemantics(window WindowSummary) weeklyResetSemantics {
	if window.ResetsAt != nil {
		return weeklyResetSemantics{kind: 2, resetNanos: window.ResetsAt.UTC().UnixNano()}
	}
	if window.SecondsUntilReset != nil {
		seconds := *window.SecondsUntilReset
		if seconds < 0 {
			seconds = 0
		}
		return weeklyResetSemantics{kind: 1, seconds: seconds}
	}
	return weeklyResetSemantics{}
}

func weeklyResetSemanticsAgree(first, second weeklyResetSemantics) bool {
	if first.kind != second.kind {
		return false
	}
	switch first.kind {
	case 0:
		return true
	case 1:
		if first.seconds > second.seconds {
			return first.seconds-second.seconds <= relativeResetDriftToleranceSeconds
		}
		return second.seconds-first.seconds <= relativeResetDriftToleranceSeconds
	case 2:
		return first.resetNanos == second.resetNanos
	default:
		return false
	}
}

func deterministicSuccessfulResultIndex(results []accountFetchResult, indexes []int) int {
	for _, index := range indexes {
		if index < 0 || index >= len(results) {
			continue
		}
		result := results[index]
		if result.fetchErr == nil && result.snapshot != nil {
			return index
		}
	}
	return -1
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
	if modelIsSpark && !hadModelWindow {
		return SelectedAccount{}, errors.New("no model-specific weekly limit available for the requested model")
	}
	if modelIsSpark {
		return SelectedAccount{}, errors.New("no model-eligible accounts available for the requested model")
	}
	if hadWeeklyWindow {
		return SelectedAccount{}, fmt.Errorf("no accounts with remaining weekly usage%s", exhaustedUsageDetail(results, model))
	}
	return SelectedAccount{}, fmt.Errorf("no usable weekly account usage available")
}

// exhaustedUsageDetail names when the blocking usage windows actually reset.
//
// The error text says "weekly" because that is the name of the window field we
// select on, NOT a promise that the wait is a week long -- the provider reports
// a rolling window whose reset is frequently minutes away. Without a reset time
// the bare string reads as "this account is gone for the week", and callers act
// on that: automated agents that hit it have abandoned a provider for the rest
// of a session when the real wait was under an hour. Every window we already
// hold carries ResetsAt/SecondsUntilReset, so state it instead of making the
// reader guess.
func exhaustedUsageDetail(results []accountFetchResult, model string) string {
	var (
		soonestSeconds int64
		haveReset      bool
		soonestAt      *time.Time
		exhaustedCount int
	)

	for _, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}
		window, _ := selectWeeklyWindowForModel(result.account, model)
		if !usageWindowAvailable(window) || !usageWindowIsKnownExhausted(window) {
			continue
		}
		exhaustedCount++

		seconds, known := secondsUntilReset(window)
		if !known {
			continue
		}
		if !haveReset || seconds < soonestSeconds {
			soonestSeconds = seconds
			soonestAt = window.ResetsAt
			haveReset = true
		}
	}

	if exhaustedCount == 0 {
		return ""
	}
	if !haveReset {
		return fmt.Sprintf(" (%d exhausted; reset time not reported by the provider)", exhaustedCount)
	}

	detail := fmt.Sprintf(" (%d exhausted; soonest reset in %s", exhaustedCount, humanizeResetDuration(soonestSeconds))
	if soonestAt != nil {
		detail += ", at " + soonestAt.UTC().Format(time.RFC3339)
	}
	return detail + ")"
}

// humanizeResetDuration renders a reset horizon so the scale is unmistakable at
// a glance: "42m" must not be mistakable for "4d02h".
func humanizeResetDuration(seconds int64) string {
	if seconds <= 0 {
		return "under a minute"
	}
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
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
	return win.UsedPercent != unavailableUsedPercent && (win.exhausted || win.UsedPercent >= 100)
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
