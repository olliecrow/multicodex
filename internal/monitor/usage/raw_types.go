package usage

import (
	"errors"
	"strings"
	"time"
)

const unavailableUsedPercent = -1

const (
	fiveHourWindowMinutes = 5 * 60
	weeklyWindowMinutes   = 7 * 24 * 60
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

func normalizeSummary(source string, snapshot rateLimitSnapshotRaw, rateLimitsByLimitID map[string]rateLimitSnapshotRaw, additionalLimitCount int, identity *identityInfo, warnings []string) (*Summary, error) {
	if snapshot.Primary == nil && snapshot.Secondary == nil {
		return nil, errors.New("missing usage windows")
	}

	now := time.Now().UTC()
	out := &Summary{
		Source:               source,
		PlanType:             snapshot.PlanType,
		WindowDataAvailable:  true,
		PrimaryWindow:        unavailableWindowSummary(),
		SecondaryWindow:      unavailableWindowSummary(),
		RateLimitWindows:     normalizeRateLimitWindows(snapshot, rateLimitsByLimitID),
		AdditionalLimitCount: additionalLimitCount,
		Warnings:             warnings,
		FetchedAt:            now,
	}
	out.PrimaryWindow, out.SecondaryWindow = normalizeWindowPair(snapshot.Primary, snapshot.Secondary)
	if identity != nil {
		out.AccountEmail = identity.Email
		out.AccountID = identity.AccountID
		out.UserID = identity.UserID
	}
	return out, nil
}

func normalizeRateLimitWindows(primary rateLimitSnapshotRaw, byLimitID map[string]rateLimitSnapshotRaw) map[string]RateLimitWindow {
	limitWindows := make(map[string]RateLimitWindow)
	rawWindows := make(map[string]rateLimitSnapshotRaw)
	for rawID, raw := range byLimitID {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if raw.Primary == nil && raw.Secondary == nil {
			continue
		}
		rawWindows[id] = raw
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

	limitWindows[primaryID] = mergeRateLimitSnapshots(primaryID, rawWindows[primaryID], primary)

	return limitWindows
}

func mergeRateLimitSnapshots(id string, preferred, fallback rateLimitSnapshotRaw) RateLimitWindow {
	window := RateLimitWindow{LimitID: id, LimitName: pointerValue(preferred.LimitName)}
	if window.LimitName == "" {
		window.LimitName = pointerValue(fallback.LimitName)
	}
	window.PrimaryWindow, window.SecondaryWindow = normalizeWindowPairs(
		fallback.Primary, fallback.Secondary,
		preferred.Primary, preferred.Secondary,
	)
	return window
}

func toRateLimitWindow(id string, raw rateLimitSnapshotRaw) RateLimitWindow {
	window := RateLimitWindow{
		LimitID:   id,
		LimitName: pointerValue(raw.LimitName),
	}
	window.PrimaryWindow, window.SecondaryWindow = normalizeWindowPair(raw.Primary, raw.Secondary)
	return window
}

func normalizeWindowPair(primary, secondary *rateLimitWindowRaw) (WindowSummary, WindowSummary) {
	return normalizeWindowPairs(primary, secondary)
}

func normalizeWindowPairs(rawWindows ...*rateLimitWindowRaw) (WindowSummary, WindowSummary) {
	fiveHour := unavailableWindowSummary()
	weekly := unavailableWindowSummary()

	// Apply positional fallbacks first. Any declared duration then wins its slot.
	for index, raw := range rawWindows {
		if raw == nil {
			continue
		}
		if pointerIntValue(raw.WindowDurationMins) != 0 {
			continue
		}
		if index%2 == 0 {
			fiveHour = toWindowSummary(raw)
		} else {
			weekly = toWindowSummary(raw)
		}
	}
	for _, raw := range rawWindows {
		if raw == nil {
			continue
		}
		switch pointerIntValue(raw.WindowDurationMins) {
		case fiveHourWindowMinutes:
			fiveHour = toWindowSummary(raw)
		case weeklyWindowMinutes:
			weekly = toWindowSummary(raw)
		}
	}
	return fiveHour, weekly
}

func pointerIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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
