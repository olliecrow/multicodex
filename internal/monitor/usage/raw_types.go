package usage

import (
	"errors"
	"strings"
	"time"
)

const unavailableUsedPercent = -1

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

func normalizeSummary(source string, snapshot rateLimitSnapshotRaw, rateLimitsByLimitID map[string]rateLimitSnapshotRaw, additionalLimitCount int, identity *identityInfo, warnings []string) (*Summary, error) {
	if snapshot.Primary == nil {
		return nil, errors.New("missing primary window")
	}

	now := time.Now().UTC()
	out := &Summary{
		Source:               source,
		PlanType:             snapshot.PlanType,
		WindowDataAvailable:  true,
		PrimaryWindow:        toWindowSummary(snapshot.Primary),
		SecondaryWindow:      unavailableWindowSummary(),
		RateLimitWindows:     normalizeRateLimitWindows(snapshot, rateLimitsByLimitID),
		AdditionalLimitCount: additionalLimitCount,
		Warnings:             warnings,
		FetchedAt:            now,
	}
	if snapshot.Secondary != nil {
		out.SecondaryWindow = toWindowSummary(snapshot.Secondary)
	}
	if identity != nil {
		out.AccountEmail = identity.Email
		out.AccountID = identity.AccountID
		out.UserID = identity.UserID
	}
	return out, nil
}

func normalizeRateLimitWindows(primary rateLimitSnapshotRaw, byLimitID map[string]rateLimitSnapshotRaw) map[string]RateLimitWindow {
	limitWindows := make(map[string]RateLimitWindow)
	for rawID, raw := range byLimitID {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if raw.Primary == nil {
			continue
		}
		limitWindows[id] = toRateLimitWindow(id, raw)
	}

	primaryID := strings.TrimSpace(primary.LimitID)
	if primaryID == "" {
		primaryID = defaultRateLimitID
	}
	if _, ok := limitWindows[primaryID]; !ok {
		limitWindows[primaryID] = toRateLimitWindow(primaryID, primary)
		return limitWindows
	}

	existing := limitWindows[primaryID]
	if existing.LimitName == "" {
		existing.LimitName = pointerValue(primary.LimitName)
	}
	if existing.PrimaryWindow.UsedPercent == unavailableUsedPercent && primary.Primary != nil {
		existing.PrimaryWindow = toWindowSummary(primary.Primary)
	}
	if existing.SecondaryWindow.UsedPercent == unavailableUsedPercent && primary.Secondary != nil {
		existing.SecondaryWindow = toWindowSummary(primary.Secondary)
	}
	limitWindows[primaryID] = existing

	return limitWindows
}

func toRateLimitWindow(id string, raw rateLimitSnapshotRaw) RateLimitWindow {
	window := RateLimitWindow{
		LimitID:   id,
		LimitName: pointerValue(raw.LimitName),
	}
	window.PrimaryWindow = toWindowSummary(raw.Primary)
	window.SecondaryWindow = unavailableWindowSummary()
	if raw.Secondary != nil {
		window.SecondaryWindow = toWindowSummary(raw.Secondary)
	}
	return window
}

func pointerValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func toWindowSummary(win *rateLimitWindowRaw) WindowSummary {
	if win == nil {
		return unavailableWindowSummary()
	}
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

func unavailableWindowSummary() WindowSummary {
	return WindowSummary{UsedPercent: unavailableUsedPercent}
}

func cloneRateLimitWindows(in map[string]RateLimitWindow) map[string]RateLimitWindow {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]RateLimitWindow, len(in))
	for id, window := range in {
		cloned := cloneRateLimitWindow(window)
		out[id] = cloned
	}
	return out
}

func cloneRateLimitWindow(window RateLimitWindow) RateLimitWindow {
	out := window
	out.PrimaryWindow = cloneWindowSummary(window.PrimaryWindow)
	out.SecondaryWindow = cloneWindowSummary(window.SecondaryWindow)
	return out
}

func cloneWindowSummary(win WindowSummary) WindowSummary {
	out := win
	if win.ResetsAt != nil {
		resetsAt := *win.ResetsAt
		out.ResetsAt = &resetsAt
	}
	if win.SecondsUntilReset != nil {
		secondsUntilReset := *win.SecondsUntilReset
		out.SecondsUntilReset = &secondsUntilReset
	}
	return out
}
