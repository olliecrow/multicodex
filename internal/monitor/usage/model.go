package usage

import "time"

// Summary is the normalized subscription usage snapshot used by CLI and TUI.
type Summary struct {
	Source                string                  `json:"source"`
	PlanType              string                  `json:"plan_type"`
	AccountEmail          string                  `json:"account_email,omitempty"`
	AccountID             string                  `json:"account_id,omitempty"`
	UserID                string                  `json:"user_id,omitempty"`
	WindowDataAvailable   bool                    `json:"window_data_available"`
	PrimaryWindow         WindowSummary           `json:"primary_window"`
	SecondaryWindow       WindowSummary           `json:"secondary_window"`
	WindowAccountLabel    string                  `json:"window_account_label,omitempty"`
	AdditionalLimitCount  int                     `json:"additional_limit_count,omitempty"`
	TotalAccounts         int                     `json:"total_accounts,omitempty"`
	SuccessfulAccounts    int                     `json:"successful_accounts,omitempty"`
	Accounts              []AccountSummary        `json:"accounts,omitempty"`
	ObservedTokens5h      *int64                  `json:"observed_tokens_5h,omitempty"`
	ObservedTokensWeekly  *int64                  `json:"observed_tokens_weekly,omitempty"`
	ObservedWindow5h      *ObservedTokenBreakdown `json:"observed_window_5h,omitempty"`
	ObservedWindowWeekly  *ObservedTokenBreakdown `json:"observed_window_weekly,omitempty"`
	ObservedTokensStatus  string                  `json:"observed_tokens_status,omitempty"`
	ObservedTokensWarming bool                    `json:"observed_tokens_warming,omitempty"`
	ObservedTokensNote    string                  `json:"observed_tokens_note,omitempty"`
	Warnings              []string                `json:"warnings,omitempty"`
	FetchedAt             time.Time               `json:"fetched_at"`
}

type WindowSummary struct {
	UsedPercent        int        `json:"used_percent"`
	WindowDurationMins *int       `json:"window_duration_mins,omitempty"`
	ResetsAt           *time.Time `json:"resets_at,omitempty"`
	SecondsUntilReset  *int64     `json:"seconds_until_reset,omitempty"`
}

type AccountSummary struct {
	Label                 string                  `json:"label"`
	Source                string                  `json:"source,omitempty"`
	PlanType              string                  `json:"plan_type,omitempty"`
	AccountEmail          string                  `json:"account_email,omitempty"`
	AccountID             string                  `json:"account_id,omitempty"`
	UserID                string                  `json:"user_id,omitempty"`
	PrimaryWindow         WindowSummary           `json:"primary_window,omitempty"`
	SecondaryWindow       WindowSummary           `json:"secondary_window,omitempty"`
	AdditionalLimitCount  int                     `json:"additional_limit_count,omitempty"`
	ObservedTokens5h      *int64                  `json:"observed_tokens_5h,omitempty"`
	ObservedTokensWeekly  *int64                  `json:"observed_tokens_weekly,omitempty"`
	ObservedWindow5h      *ObservedTokenBreakdown `json:"observed_window_5h,omitempty"`
	ObservedWindowWeekly  *ObservedTokenBreakdown `json:"observed_window_weekly,omitempty"`
	ObservedTokensStatus  string                  `json:"observed_tokens_status,omitempty"`
	ObservedTokensWarming bool                    `json:"observed_tokens_warming,omitempty"`
	ObservedTokensNote    string                  `json:"observed_tokens_note,omitempty"`
	Warnings              []string                `json:"warnings,omitempty"`
	Error                 string                  `json:"error,omitempty"`
	FetchedAt             *time.Time              `json:"fetched_at,omitempty"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Details string `json:"details"`
}
