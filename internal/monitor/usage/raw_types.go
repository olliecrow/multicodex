package usage

import (
	"errors"
	"time"
)

type rateLimitWindowRaw struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int   `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type creditsSnapshotRaw struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type rateLimitSnapshotRaw struct {
	LimitID   string              `json:"limitId"`
	LimitName *string             `json:"limitName"`
	PlanType  string              `json:"planType"`
	Primary   *rateLimitWindowRaw `json:"primary"`
	Secondary *rateLimitWindowRaw `json:"secondary"`
	Credits   *creditsSnapshotRaw `json:"credits"`
}

type rateLimitsReadResultRaw struct {
	RateLimits          rateLimitSnapshotRaw            `json:"rateLimits"`
	RateLimitsByLimitID map[string]rateLimitSnapshotRaw `json:"rateLimitsByLimitId"`
}

type identityInfo struct {
	Email     string
	AccountID string
	UserID    string
}

func normalizeSummary(source string, snapshot rateLimitSnapshotRaw, additionalLimitCount int, identity *identityInfo, warnings []string) (*Summary, error) {
	if snapshot.Primary == nil {
		return nil, errors.New("missing primary window")
	}
	if snapshot.Secondary == nil {
		return nil, errors.New("missing secondary window")
	}

	now := time.Now().UTC()
	out := &Summary{
		Source:               source,
		PlanType:             snapshot.PlanType,
		WindowDataAvailable:  true,
		PrimaryWindow:        toWindowSummary(snapshot.Primary),
		SecondaryWindow:      toWindowSummary(snapshot.Secondary),
		AdditionalLimitCount: additionalLimitCount,
		Warnings:             warnings,
		FetchedAt:            now,
	}
	if identity != nil {
		out.AccountEmail = identity.Email
		out.AccountID = identity.AccountID
		out.UserID = identity.UserID
	}
	return out, nil
}

func toWindowSummary(win *rateLimitWindowRaw) WindowSummary {
	out := WindowSummary{
		UsedPercent:        win.UsedPercent,
		WindowDurationMins: win.WindowDurationMins,
	}
	if win.ResetsAt != nil {
		reset := time.Unix(*win.ResetsAt, 0).UTC()
		out.ResetsAt = &reset
		seconds := int64(time.Until(reset).Seconds())
		out.SecondsUntilReset = &seconds
	}
	return out
}
