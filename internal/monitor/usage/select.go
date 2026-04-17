package usage

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"time"
)

type SelectedAccount struct {
	Account                      MonitorAccount
	PrimaryUsedPercent           int
	SecondaryUsedPercent         int
	UsedPrimaryThresholdFallback bool
}

const (
	weeklyResetBucketWithin24Hours = iota
	weeklyResetBucketWithin72Hours
	weeklyResetBucketAfter72Hours
	weeklyResetBucketUnknown
	weeklyResetBucketCount
)

const (
	weeklyResetWithin24HoursSeconds = int64(24 * 60 * 60)
	weeklyResetWithin72HoursSeconds = int64(72 * 60 * 60)
)

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
	safeBuckets := make([][]int, weeklyResetBucketCount)
	fallbackCandidates := []int{}
	lowestPrimaryUsedPercent := 0
	haveFallbackCandidate := false

	for i, result := range results {
		if result.fetchErr != nil || result.snapshot == nil {
			continue
		}

		primaryUsedPercent := result.account.PrimaryWindow.UsedPercent
		if !haveFallbackCandidate || primaryUsedPercent < lowestPrimaryUsedPercent {
			lowestPrimaryUsedPercent = primaryUsedPercent
			fallbackCandidates = []int{i}
			haveFallbackCandidate = true
		} else if primaryUsedPercent == lowestPrimaryUsedPercent {
			fallbackCandidates = append(fallbackCandidates, i)
		}

		if primaryUsedPercent >= maxPrimaryUsedPercent {
			continue
		}

		bucket := weeklyResetBucketForWindow(result.account.SecondaryWindow)
		safeBuckets[bucket] = append(safeBuckets[bucket], i)
	}

	for _, candidates := range safeBuckets {
		if len(candidates) == 0 {
			continue
		}
		chosenIndex := chooseRandomResultIndex(candidates)
		chosen := results[chosenIndex]
		return SelectedAccount{
			Account:              MonitorAccount{Label: chosen.account.Label, CodexHome: chosen.codexHome},
			PrimaryUsedPercent:   chosen.account.PrimaryWindow.UsedPercent,
			SecondaryUsedPercent: chosen.account.SecondaryWindow.UsedPercent,
		}, nil
	}

	chosenIndex := chooseRandomResultIndex(fallbackCandidates)
	if chosenIndex != -1 {
		chosen := results[chosenIndex]
		return SelectedAccount{
			Account:                      MonitorAccount{Label: chosen.account.Label, CodexHome: chosen.codexHome},
			PrimaryUsedPercent:           chosen.account.PrimaryWindow.UsedPercent,
			SecondaryUsedPercent:         chosen.account.SecondaryWindow.UsedPercent,
			UsedPrimaryThresholdFallback: true,
		}, nil
	}

	return SelectedAccount{}, fmt.Errorf("no accessible accounts")
}

func weeklyResetBucketForWindow(win WindowSummary) int {
	secondsUntilReset, ok := secondsUntilReset(win)
	if !ok {
		return weeklyResetBucketUnknown
	}

	switch {
	case secondsUntilReset <= weeklyResetWithin24HoursSeconds:
		return weeklyResetBucketWithin24Hours
	case secondsUntilReset <= weeklyResetWithin72HoursSeconds:
		return weeklyResetBucketWithin72Hours
	default:
		return weeklyResetBucketAfter72Hours
	}
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
