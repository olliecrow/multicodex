package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Fetcher struct {
	primary  Source
	fallback Source

	accounts                []accountFetcher
	observed                tokenEstimator
	initializationNote      string
	accountLoader           func() ([]MonitorAccount, string, error)
	accountRefreshInterval  time.Duration
	accountsLastRefreshedAt time.Time
}

const (
	unverifiedAccountIdentityKey = "unverified"
	maxFallbackReserve           = 10 * time.Second
)

type accountFetcher struct {
	account  MonitorAccount
	primary  Source
	fallback Source
}

type accountFetchResult struct {
	codexHome           string
	selectionPriority   int
	account             AccountSummary
	snapshot            *Summary
	fetchErr            error
	observedAvailable   bool
	observedUnavailable bool
	warnings            []string
}

type tokenEstimator interface {
	Estimate(codexHome string, now time.Time) (ObservedTokenEstimate, error)
}

func NewDefaultFetcher() *Fetcher {
	return newConfiguredFetcher(true)
}

func NewDefaultFetcherWithAccountOptions(options MonitorAccountOptions) *Fetcher {
	return newConfiguredFetcherWithLoader(true, func() ([]MonitorAccount, string, error) {
		return loadMonitorAccountsWithOptions(options)
	})
}

func NewSnapshotFetcher() *Fetcher {
	return newConfiguredFetcherWithLoader(false, loadMonitorAccounts)
}

func newConfiguredFetcher(asyncObserved bool) *Fetcher {
	return newConfiguredFetcherWithLoader(asyncObserved, loadMonitorAccounts)
}

func newConfiguredFetcherWithLoader(asyncObserved bool, loader func() ([]MonitorAccount, string, error)) *Fetcher {
	f := &Fetcher{
		observed:               newObservedTokenEstimator(60*time.Second, asyncObserved),
		accountLoader:          loader,
		accountRefreshInterval: 60 * time.Second,
	}
	f.refreshAccounts(time.Now().UTC(), true)
	return f
}

func (f *Fetcher) Fetch(ctx context.Context) (*Summary, error) {
	if f.accountLoader != nil {
		f.refreshAccounts(time.Now().UTC(), len(f.accounts) == 0)
	}
	if len(f.accounts) > 0 {
		return f.fetchMultiAccount(ctx)
	}
	if f.accountLoader != nil {
		msg := "no monitor accounts configured"
		if f.initializationNote != "" {
			msg += ": " + f.initializationNote
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return f.fetchSingle(ctx)
}

func (f *Fetcher) fetchSingle(ctx context.Context) (*Summary, error) {
	if f.primary == nil {
		return nil, fmt.Errorf("missing primary source")
	}

	primarySummary, primaryErr := fetchWithFallback(ctx, f.primary, f.fallback)
	if primaryErr != nil {
		return nil, primaryErr
	}
	return primarySummary, nil
}

func (f *Fetcher) fetchMultiAccount(ctx context.Context) (*Summary, error) {
	now := time.Now().UTC()
	f.refreshAccounts(now, false)

	out := &Summary{
		ObservedTokensStatus: observedTokensStatusUnavailable,
		FetchedAt:            now,
	}
	if f.initializationNote != "" {
		out.Warnings = append(out.Warnings, f.initializationNote)
	}

	anyAccountSuccess := false
	anyObservedAvailable := false
	anyObservedWarming := false
	unavailableObservedCount := 0
	totalAccountIdentities := map[string]struct{}{}
	successfulAccountIdentities := map[string]struct{}{}
	seenObservedByIdentity := map[string]ObservedTokenBreakdown{}
	accountByIdentity := map[string]accountSummaryWithHome{}
	activeHomes := resolveActiveCodexHomes()
	var activeSuccessChoice *accountFetchResult
	var activeLabelChoice *accountSummaryWithHome
	activeHomeDiscovered := false
	activeFetchFailed := false

	results := f.fetchAccountsConcurrent(ctx, now, activeHomes)
	for _, result := range results {
		accountOut := result.account
		accountIdentity := accountIdentityOrHomeKey(accountOut, result.codexHome)
		totalAccountIdentities[accountIdentity] = struct{}{}
		if activeHomes.matches(result.codexHome) {
			activeHomeDiscovered = true
			if activeLabelChoice == nil || shouldPreferAccountSummary(*activeLabelChoice, accountOut, result.codexHome, activeHomes) {
				choice := accountSummaryWithHome{
					account:   accountOut,
					codexHome: result.codexHome,
				}
				activeLabelChoice = &choice
			}
		}
		if result.fetchErr != nil {
			out.Warnings = append(out.Warnings, accountFetchWarning(accountOut.Label, result.fetchErr))
			if activeHomes.matches(result.codexHome) {
				activeFetchFailed = true
			}
		} else if result.snapshot != nil {
			anyAccountSuccess = true
			successfulAccountIdentities[accountIdentity] = struct{}{}
			if activeHomes.matches(result.codexHome) {
				if activeSuccessChoice == nil || shouldPreferAccountSummary(accountSummaryWithHome{
					account:   activeSuccessChoice.account,
					codexHome: activeSuccessChoice.codexHome,
				}, accountOut, result.codexHome, activeHomes) {
					candidate := result
					activeSuccessChoice = &candidate
				}
			}
		}
		if result.observedAvailable {
			anyObservedAvailable = true
			weekly := ObservedTokenBreakdown{}
			if accountOut.ObservedWindowWeekly != nil {
				weekly = *accountOut.ObservedWindowWeekly
			}

			identity := accountIdentityOrHomeKey(accountOut, result.codexHome)
			seenObservedByIdentity[identity] = addBreakdowns(seenObservedByIdentity[identity], weekly)
		}
		if result.observedUnavailable {
			unavailableObservedCount++
		}
		if result.account.ObservedTokensWarming {
			anyObservedWarming = true
		}
		out.Warnings = append(out.Warnings, result.warnings...)
		existing, ok := accountByIdentity[accountIdentity]
		if !ok || shouldPreferAccountSummary(existing, accountOut, result.codexHome, activeHomes) {
			accountByIdentity[accountIdentity] = accountSummaryWithHome{
				account:   accountOut,
				codexHome: result.codexHome,
			}
		}
	}
	out.Accounts = accountSummariesFromIdentityMap(accountByIdentity)
	out.TotalAccounts = len(totalAccountIdentities)
	out.SuccessfulAccounts = len(successfulAccountIdentities)
	if activeLabelChoice != nil {
		out.WindowAccountLabel = activeLabelChoice.account.Label
	}

	if activeSuccessChoice != nil && activeSuccessChoice.snapshot != nil {
		activeSuccess := activeSuccessChoice.snapshot
		out.Source = activeSuccess.Source
		out.PlanType = activeSuccess.PlanType
		out.AccountEmail = activeSuccess.AccountEmail
		out.AccountID = activeSuccess.AccountID
		out.UserID = activeSuccess.UserID
		out.RateLimitWindows = cloneRateLimitWindows(activeSuccess.RateLimitWindows)
		out.WindowDataAvailable = summaryHasWeeklyData(activeSuccess)
		out.WeeklyWindow = activeSuccess.WeeklyWindow
		out.AdditionalLimitCount = activeSuccess.AdditionalLimitCount
		out.FetchedAt = activeSuccess.FetchedAt
	} else {
		out.WindowDataAvailable = false
		switch {
		case activeHomes.primary == "":
			out.Warnings = append(out.Warnings, "active account home is unavailable; window cards are unavailable")
		case !activeHomeDiscovered:
			out.Warnings = append(out.Warnings, activeHomeNotMonitoredWarning())
		case activeFetchFailed:
			out.Warnings = append(out.Warnings, "active account usage fetch failed; window cards are unavailable")
		default:
			out.Warnings = append(out.Warnings, "active account usage is unavailable; window cards are unavailable")
		}
	}

	if anyObservedAvailable {
		observedTotal := ObservedTokenBreakdown{}
		for _, weekly := range seenObservedByIdentity {
			observedTotal = addBreakdowns(observedTotal, weekly)
		}
		out.ObservedTokensStatus = observedTokensStatusEstimated
		out.ObservedWindowWeekly = &observedTotal
		out.ObservedTokensWeekly = int64Ptr(observedTotal.Total)
		out.ObservedTokensNote = "sum across accounts"
		out.ObservedTokensWarming = false
		if unavailableObservedCount > 0 {
			out.ObservedTokensStatus = observedTokensStatusPartial
			out.ObservedTokensNote = "partial sum across accounts; some account homes unavailable"
		}
	} else if unavailableObservedCount > 0 {
		out.ObservedTokensStatus = observedTokensStatusUnavailable
		out.ObservedTokensNote = "token estimate warming or unavailable"
		out.ObservedTokensWarming = anyObservedWarming
	}

	out.Warnings = dedupeStrings(out.Warnings)

	if !anyAccountSuccess && !anyObservedAvailable {
		return nil, fmt.Errorf("all account fetches failed and observed tokens are unavailable")
	}
	return out, nil
}

func activeHomeNotMonitoredWarning() string {
	if strings.TrimSpace(os.Getenv("CODEX_HOME")) != "" {
		return "active CODEX_HOME is not in monitored accounts; rerun with --include-active to show its window cards"
	}
	return "global Codex home is not in monitored accounts; rerun with --include-default to show its window cards"
}

func fetchWithFallback(ctx context.Context, primary Source, fallback Source) (*Summary, error) {
	if primary == nil {
		return nil, fmt.Errorf("missing primary source")
	}

	primaryCtx, cancelPrimary := attemptContext(ctx, fallback != nil)
	primarySummary, primaryErr := primary.Fetch(primaryCtx)
	cancelPrimary()
	if primaryErr == nil {
		return primarySummary, nil
	}

	if fallback == nil {
		return nil, fmt.Errorf("primary source %q failed: %w", primary.Name(), primaryErr)
	}

	fallbackCtx, cancelFallback := attemptContext(ctx, false)
	fallbackSummary, fallbackErr := fallback.Fetch(fallbackCtx)
	cancelFallback()
	if fallbackErr == nil {
		fallbackSummary.Warnings = append(fallbackSummary.Warnings, fmt.Sprintf("primary source %q failed: %v", primary.Name(), primaryErr))
		return fallbackSummary, nil
	}

	return nil, fmt.Errorf(
		"primary source %q failed: %v; fallback source %q failed: %v",
		primary.Name(), primaryErr, fallback.Name(), fallbackErr,
	)
}

func accountFetchWarning(label string, err error) string {
	if err == nil {
		return ""
	}
	if simplified := simplifiedAuthWarning(label, err); simplified != "" {
		return simplified
	}
	return fmt.Sprintf("account %q fetch failed: %v", label, err)
}

func simplifiedAuthWarning(label string, err error) string {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case msg == "":
		return ""
	case strings.Contains(msg, "token_expired"),
		strings.Contains(msg, "authentication expired"),
		strings.Contains(msg, "provided authentication token is expired"),
		strings.Contains(msg, "auth token is expired"):
		return fmt.Sprintf("account %q auth expired; sign in again", label)
	case strings.Contains(msg, "authentication rejected"),
		strings.Contains(msg, "401 unauthorized") && strings.Contains(msg, "sign in again"):
		return fmt.Sprintf("account %q auth rejected; sign in again", label)
	default:
		return ""
	}
}

func attemptContext(parent context.Context, reserveForFallback bool) (context.Context, context.CancelFunc) {
	deadline, ok := parent.Deadline()
	if !ok {
		return parent, func() {}
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return parent, func() {}
	}
	if reserveForFallback {
		reserve := remaining / 2
		if reserve > maxFallbackReserve {
			reserve = maxFallbackReserve
		}
		remaining -= reserve
	}
	if remaining <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, remaining)
}

func (f *Fetcher) Close() error {
	var firstErr error
	for _, account := range f.accounts {
		if account.primary != nil {
			if err := account.primary.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if account.fallback != nil {
			if err := account.fallback.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if f.primary != nil {
		if err := f.primary.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if f.fallback != nil {
		if err := f.fallback.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f *Fetcher) Primary() Source {
	return f.primary
}

func (f *Fetcher) Fallback() Source {
	return f.fallback
}

func int64Ptr(v int64) *int64 {
	out := v
	return &out
}

func (f *Fetcher) refreshAccounts(now time.Time, force bool) {
	if f.accountLoader == nil {
		return
	}
	if !force && f.accountRefreshInterval > 0 && !f.accountsLastRefreshedAt.IsZero() {
		if now.Sub(f.accountsLastRefreshedAt) < f.accountRefreshInterval {
			return
		}
	}

	accounts, warning, err := f.accountLoader()
	f.accountsLastRefreshedAt = now
	if err != nil {
		f.initializationNote = err.Error()
		return
	}

	f.initializationNote = warning
	f.replaceAccountFetchers(accounts)
}

func (f *Fetcher) replaceAccountFetchers(accounts []MonitorAccount) {
	existingByHome := map[string]accountFetcher{}
	for _, account := range f.accounts {
		home := normalizeHome(account.account.CodexHome)
		if home == "" {
			continue
		}
		existingByHome[home] = account
	}

	usedHomes := map[string]struct{}{}
	next := make([]accountFetcher, 0, len(accounts))
	for _, account := range accounts {
		home := normalizeHome(account.CodexHome)
		if home == "" {
			continue
		}
		account.CodexHome = home
		if existing, ok := existingByHome[home]; ok {
			if existing.account.UseAppServer != account.UseAppServer {
				closeAccountFetcher(existing)
				next = append(next, newAccountFetcher(account))
				usedHomes[home] = struct{}{}
				continue
			}
			existing.account = account
			next = append(next, existing)
			usedHomes[home] = struct{}{}
			continue
		}

		next = append(next, newAccountFetcher(account))
		usedHomes[home] = struct{}{}
	}

	for home, existing := range existingByHome {
		if _, ok := usedHomes[home]; ok {
			continue
		}
		closeAccountFetcher(existing)
	}
	f.accounts = next
}

func closeAccountFetcher(fetcher accountFetcher) {
	if fetcher.primary != nil {
		_ = fetcher.primary.Close()
	}
	if fetcher.fallback != nil {
		_ = fetcher.fallback.Close()
	}
}

func newAccountFetcher(account MonitorAccount) accountFetcher {
	fetcher := accountFetcher{
		account: account,
		primary: NewOAuthSourceForHome(account.CodexHome),
	}
	if account.UseAppServer {
		fetcher.primary = NewAppServerSourceForHome(account.CodexHome)
		fetcher.fallback = NewOAuthSourceForHome(account.CodexHome)
	}
	return fetcher
}

func normalizeHome(home string) string {
	trimmed := strings.TrimSpace(home)
	if trimmed == "" {
		return ""
	}
	normalized := filepath.Clean(trimmed)
	if abs, err := filepath.Abs(normalized); err == nil {
		normalized = abs
	}
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil && strings.TrimSpace(resolved) != "" {
		normalized = resolved
	}
	return filepath.Clean(normalized)
}

func resolveActiveCodexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		expanded, err := expandPath(home)
		if err == nil {
			return normalizeHome(expanded)
		}
	}
	home, err := defaultCodexHome()
	if err != nil {
		return ""
	}
	return normalizeHome(home)
}

type activeHomeSet struct {
	primary string
	homes   map[string]struct{}
}

func resolveActiveCodexHomes() activeHomeSet {
	primary := resolveActiveCodexHome()
	out := activeHomeSet{
		primary: primary,
		homes:   map[string]struct{}{},
	}
	if primary == "" {
		return out
	}
	out.homes[primary] = struct{}{}
	return out
}

func (s activeHomeSet) matches(home string) bool {
	if len(s.homes) == 0 {
		return false
	}
	_, ok := s.homes[normalizeHome(home)]
	return ok
}

func identityKey(email, accountID, userID string) string {
	if v := strings.TrimSpace(email); v != "" {
		return "email:" + strings.ToLower(v)
	}
	if v := strings.TrimSpace(accountID); v != "" {
		return "account_id:" + strings.ToLower(v)
	}
	if v := strings.TrimSpace(userID); v != "" {
		return "user_id:" + strings.ToLower(v)
	}
	return ""
}

func accountIdentityOrHomeKey(account AccountSummary, codexHome string) string {
	if identity := identityKey(account.AccountEmail, account.AccountID, account.UserID); identity != "" {
		return identity
	}
	if identity := authIdentityKeyForHome(codexHome); identity != "" {
		return identity
	}
	if home := normalizeHome(codexHome); home != "" {
		return "home:" + strings.ToLower(home)
	}
	if label := strings.TrimSpace(account.Label); label != "" {
		return "label:" + strings.ToLower(label)
	}
	return unverifiedAccountIdentityKey
}

type accountSummaryWithHome struct {
	account   AccountSummary
	codexHome string
}

func shouldPreferAccountSummary(existing accountSummaryWithHome, candidate AccountSummary, candidateHome string, activeHomes activeHomeSet) bool {
	existingOK := strings.TrimSpace(existing.account.Error) == ""
	candidateOK := strings.TrimSpace(candidate.Error) == ""
	if existingOK != candidateOK {
		return candidateOK
	}

	existingActive := activeHomes.matches(existing.codexHome)
	candidateActive := activeHomes.matches(candidateHome)
	if existingActive != candidateActive {
		return candidateActive
	}
	existingSyntheticDefault := isSyntheticDefaultAlias(existing.account.Label, existing.codexHome, activeHomes)
	candidateSyntheticDefault := isSyntheticDefaultAlias(candidate.Label, candidateHome, activeHomes)
	if existingSyntheticDefault != candidateSyntheticDefault {
		return !candidateSyntheticDefault
	}

	if existing.account.FetchedAt == nil {
		return candidate.FetchedAt != nil
	}
	if candidate.FetchedAt == nil {
		return false
	}
	return candidate.FetchedAt.After(*existing.account.FetchedAt)
}

func isSyntheticDefaultAlias(label, codexHome string, activeHomes activeHomeSet) bool {
	return activeHomes.primary != "" &&
		normalizeHome(codexHome) == activeHomes.primary &&
		strings.EqualFold(strings.TrimSpace(label), "default")
}

func accountSummariesFromIdentityMap(byIdentity map[string]accountSummaryWithHome) []AccountSummary {
	if len(byIdentity) == 0 {
		return nil
	}
	accounts := make([]AccountSummary, 0, len(byIdentity))
	for _, row := range byIdentity {
		accounts = append(accounts, row.account)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Label != accounts[j].Label {
			return accounts[i].Label < accounts[j].Label
		}
		if accounts[i].AccountEmail != accounts[j].AccountEmail {
			return accounts[i].AccountEmail < accounts[j].AccountEmail
		}
		return accounts[i].Source < accounts[j].Source
	})
	return accounts
}

func addBreakdowns(a, b ObservedTokenBreakdown) ObservedTokenBreakdown {
	return ObservedTokenBreakdown{
		Total:           a.Total + b.Total,
		Input:           a.Input + b.Input,
		CachedInput:     a.CachedInput + b.CachedInput,
		Output:          a.Output + b.Output,
		ReasoningOutput: a.ReasoningOutput + b.ReasoningOutput,
		CachedOutput:    a.CachedOutput + b.CachedOutput,
		HasSplit:        a.HasSplit || b.HasSplit,
		HasCachedOutput: a.HasCachedOutput || b.HasCachedOutput,
	}
}

func (f *Fetcher) fetchAccountsConcurrent(ctx context.Context, now time.Time, activeHomes activeHomeSet) []accountFetchResult {
	if len(f.accounts) == 0 {
		return nil
	}

	results := make([]accountFetchResult, len(f.accounts))
	parallelism := len(f.accounts)
	if parallelism > 4 {
		parallelism = 4
	}

	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	fetchOne := func(i int, account accountFetcher, usePool bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if usePool {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			results[i] = f.fetchAccountResult(ctx, account, now)
		}()
	}

	for i, account := range f.accounts {
		fetchOne(i, account, !activeHomes.matches(account.account.CodexHome))
	}
	wg.Wait()
	return results
}

func (f *Fetcher) fetchAccountResult(ctx context.Context, account accountFetcher, now time.Time) accountFetchResult {
	result := accountFetchResult{
		codexHome:         account.account.CodexHome,
		selectionPriority: account.account.SelectionPriority,
		account: AccountSummary{
			Label: account.account.Label,
		},
	}

	snapshot, fetchErr := fetchWithFallback(ctx, account.primary, account.fallback)
	if fetchErr != nil {
		result.fetchErr = fetchErr
		if email, err := accountEmailFromAuthFileForHome(account.account.CodexHome); err == nil {
			result.account.AccountEmail = email
		}
		result.account.Error = fetchErr.Error()
	} else {
		result.snapshot = snapshot
		result.account.Source = snapshot.Source
		result.account.PlanType = snapshot.PlanType
		result.account.AccountEmail = snapshot.AccountEmail
		result.account.AccountID = snapshot.AccountID
		result.account.UserID = snapshot.UserID
		result.account.WeeklyWindow = snapshot.WeeklyWindow
		result.account.RateLimitWindows = cloneRateLimitWindows(snapshot.RateLimitWindows)
		result.account.AdditionalLimitCount = snapshot.AdditionalLimitCount
		result.account.Warnings = append(result.account.Warnings, snapshot.Warnings...)
		ts := snapshot.FetchedAt
		result.account.FetchedAt = &ts
	}

	if f.observed != nil {
		estimate, estimateErr := f.observed.Estimate(account.account.CodexHome, now)
		if estimateErr != nil {
			result.account.ObservedTokensStatus = observedTokensStatusUnavailable
			result.account.ObservedTokensNote = estimate.Note
			result.account.ObservedTokensWarming = estimate.Warming
			result.observedUnavailable = true
			result.warnings = append(result.warnings, fmt.Sprintf("account %q observed tokens unavailable: %v", account.account.Label, estimateErr))
		} else {
			result.account.ObservedTokensStatus = estimate.Status
			result.account.ObservedTokensNote = estimate.Note
			result.account.ObservedTokensWarming = estimate.Warming
			result.account.Warnings = append(result.account.Warnings, estimate.Warnings...)
			result.account.ObservedWindowWeekly = &estimate.WindowWeekly
			result.account.ObservedTokensWeekly = int64Ptr(estimate.WindowWeekly.Total)

			if estimate.Status == observedTokensStatusUnavailable {
				result.observedUnavailable = true
			} else {
				result.observedAvailable = true
			}
		}
	}

	result.account.Warnings = dedupeStrings(result.account.Warnings)
	return result
}
