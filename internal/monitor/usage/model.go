package usage

import (
	"sort"
	"strings"
	"time"
)

const defaultRateLimitID = "codex"

// Summary is the normalized subscription usage snapshot used by CLI and TUI.
type Summary struct {
	Source                string                     `json:"source"`
	PlanType              string                     `json:"plan_type"`
	AccountEmail          string                     `json:"account_email,omitempty"`
	AccountID             string                     `json:"account_id,omitempty"`
	UserID                string                     `json:"user_id,omitempty"`
	WindowDataAvailable   bool                       `json:"window_data_available"`
	PrimaryWindow         WindowSummary              `json:"primary_window"`
	SecondaryWindow       WindowSummary              `json:"secondary_window"`
	WindowAccountLabel    string                     `json:"window_account_label,omitempty"`
	AdditionalLimitCount  int                        `json:"additional_limit_count,omitempty"`
	RateLimitWindows      map[string]RateLimitWindow `json:"rate_limit_windows,omitempty"`
	TotalAccounts         int                        `json:"total_accounts,omitempty"`
	SuccessfulAccounts    int                        `json:"successful_accounts,omitempty"`
	Accounts              []AccountSummary           `json:"accounts,omitempty"`
	ObservedTokens5h      *int64                     `json:"observed_tokens_5h,omitempty"`
	ObservedTokensWeekly  *int64                     `json:"observed_tokens_weekly,omitempty"`
	ObservedWindow5h      *ObservedTokenBreakdown    `json:"observed_window_5h,omitempty"`
	ObservedWindowWeekly  *ObservedTokenBreakdown    `json:"observed_window_weekly,omitempty"`
	ObservedTokensStatus  string                     `json:"observed_tokens_status,omitempty"`
	ObservedTokensWarming bool                       `json:"observed_tokens_warming,omitempty"`
	ObservedTokensNote    string                     `json:"observed_tokens_note,omitempty"`
	Warnings              []string                   `json:"warnings,omitempty"`
	FetchedAt             time.Time                  `json:"fetched_at"`
}

type WindowSummary struct {
	UsedPercent        int        `json:"used_percent"`
	WindowDurationMins *int       `json:"window_duration_mins,omitempty"`
	ResetsAt           *time.Time `json:"resets_at,omitempty"`
	SecondsUntilReset  *int64     `json:"seconds_until_reset,omitempty"`
}

type AccountSummary struct {
	Label                 string                     `json:"label"`
	Source                string                     `json:"source,omitempty"`
	PlanType              string                     `json:"plan_type,omitempty"`
	AccountEmail          string                     `json:"account_email,omitempty"`
	AccountID             string                     `json:"account_id,omitempty"`
	UserID                string                     `json:"user_id,omitempty"`
	PrimaryWindow         WindowSummary              `json:"primary_window,omitempty"`
	SecondaryWindow       WindowSummary              `json:"secondary_window,omitempty"`
	AdditionalLimitCount  int                        `json:"additional_limit_count,omitempty"`
	RateLimitWindows      map[string]RateLimitWindow `json:"rate_limit_windows,omitempty"`
	ObservedTokens5h      *int64                     `json:"observed_tokens_5h,omitempty"`
	ObservedTokensWeekly  *int64                     `json:"observed_tokens_weekly,omitempty"`
	ObservedWindow5h      *ObservedTokenBreakdown    `json:"observed_window_5h,omitempty"`
	ObservedWindowWeekly  *ObservedTokenBreakdown    `json:"observed_window_weekly,omitempty"`
	ObservedTokensStatus  string                     `json:"observed_tokens_status,omitempty"`
	ObservedTokensWarming bool                       `json:"observed_tokens_warming,omitempty"`
	ObservedTokensNote    string                     `json:"observed_tokens_note,omitempty"`
	Warnings              []string                   `json:"warnings,omitempty"`
	Error                 string                     `json:"error,omitempty"`
	FetchedAt             *time.Time                 `json:"fetched_at,omitempty"`
}

type RateLimitWindow struct {
	LimitID         string        `json:"limit_id"`
	LimitName       string        `json:"limit_name,omitempty"`
	PrimaryWindow   WindowSummary `json:"primary_window"`
	SecondaryWindow WindowSummary `json:"secondary_window"`
}

func (w RateLimitWindow) displayName() string {
	trimmedName := strings.TrimSpace(w.LimitName)
	if trimmedName != "" {
		return trimmedName
	}
	trimmedID := strings.TrimSpace(w.LimitID)
	if trimmedID != "" {
		return trimmedID
	}
	return ""
}

func (s *Summary) RateLimitWindowForModel(model string) (string, RateLimitWindow, bool) {
	if s == nil || len(s.RateLimitWindows) == 0 {
		return "", RateLimitWindow{}, false
	}

	id, ok := selectRateLimitWindowID(s.RateLimitWindows, model)
	if !ok {
		return "", RateLimitWindow{}, false
	}
	return id, s.RateLimitWindows[id], true
}

func (s *AccountSummary) RateLimitWindowForModel(model string) (string, RateLimitWindow, bool) {
	if len(s.RateLimitWindows) == 0 {
		return "", RateLimitWindow{}, false
	}
	id, ok := selectRateLimitWindowID(s.RateLimitWindows, model)
	if !ok {
		return "", RateLimitWindow{}, false
	}
	return id, s.RateLimitWindows[id], true
}

func (s *Summary) RateLimitWindowIDs() []string {
	return sortedRateLimitWindowKeys(s.RateLimitWindows)
}

func (s *AccountSummary) RateLimitWindowIDs() []string {
	return sortedRateLimitWindowKeys(s.RateLimitWindows)
}

func selectRateLimitWindowID(windows map[string]RateLimitWindow, model string) (string, bool) {
	if len(windows) == 0 {
		return "", false
	}

	isSpark := isSparkModel(model)
	sparkID := firstRateLimitWindowID(windows, func(id, name string) bool {
		return isSparkLimitBucket(id, name)
	})
	if isSpark && sparkID != "" {
		return sparkID, true
	}

	if _, ok := windows[defaultRateLimitID]; ok {
		return defaultRateLimitID, true
	}

	return "", false
}

func firstRateLimitWindowID(windows map[string]RateLimitWindow, matches func(id, name string) bool) string {
	for _, limitID := range sortedRateLimitWindowKeys(windows) {
		window := windows[limitID]
		if matches(limitID, window.LimitName) {
			return limitID
		}
	}
	return ""
}

func sortedRateLimitWindowKeys(windows map[string]RateLimitWindow) []string {
	if len(windows) == 0 {
		return nil
	}
	out := make([]string, 0, len(windows))
	for k := range windows {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isSparkModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "spark")
}

func isSparkLimitBucket(limitID, limitName string) bool {
	normalizedID := strings.ToLower(strings.TrimSpace(limitID))
	if strings.Contains(normalizedID, "spark") || strings.Contains(normalizedID, "bengalfox") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(limitName)), "spark")
}

func isDefaultRateLimitID(limitID string) bool {
	return strings.EqualFold(strings.TrimSpace(limitID), defaultRateLimitID)
}

type DoctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Details string `json:"details"`
}
