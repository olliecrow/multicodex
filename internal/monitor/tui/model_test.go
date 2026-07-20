package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/olliecrow/multicodex/internal/monitor/usage"
)

func TestWeeklyAccountCardShowsDefaultSparkAndExactReset(t *testing.T) {
	m := fixtureModel(112, 24, true)
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"weekly usage [alpha]", "default", "35%", "Spark", "62%",
		"resets in", "Mon 20 Jul 14:30", "[████",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in view:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"five-hour", "remaining", "primary window", "secondary window"} {
		if strings.Contains(strings.ToLower(view), unwanted) {
			t.Fatalf("did not expect %q in weekly-only view:\n%s", unwanted, view)
		}
	}
}

func TestWeeklyAccountCardNarrowViewKeepsCoreValuesAndHidesDecoration(t *testing.T) {
	m := fixtureModel(42, 18, true)
	view := ansi.Strip(m.View())
	for _, want := range []string{"weekly usage [alpha]", "default", "35%", "Spark", "62%", "resets"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow view to keep %q:\n%s", want, view)
		}
	}
	for _, want := range []string{"multicodex", "healthy", "<1m", "12:00"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow header to keep %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "████") || strings.Contains(view, "Mon 20 Jul") {
		t.Fatalf("expected narrow view to hide optional decoration:\n%s", view)
	}
	assertViewport(t, view, 42, 18)
}

func TestWeeklyAccountCardUnavailableAndPartialSparkStates(t *testing.T) {
	m := fixtureModel(90, 20, true)
	m.summary.WeeklyWindow = usage.WindowSummary{UsedPercent: -1}
	defaultBucket := m.summary.RateLimitWindows["codex"]
	defaultBucket.WeeklyWindow = usage.WindowSummary{UsedPercent: -1}
	m.summary.RateLimitWindows["codex"] = defaultBucket
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "default unavailable") {
		t.Fatalf("expected unavailable default weekly line:\n%s", view)
	}
	if !strings.Contains(view, "Spark") || !strings.Contains(view, "62%") {
		t.Fatalf("expected Spark weekly line to remain available:\n%s", view)
	}
}

func TestAccountCardsOrderKnownWeeklyResetsThenUnknown(t *testing.T) {
	m := fixtureModel(90, 28, true)
	m.summary.WindowAccountLabel = ""
	m.summary.Accounts = []usage.AccountSummary{
		accountFixture("unknown", 10, nil, true),
		accountFixture("later", 20, int64Ptr(600), true),
		accountFixture("sooner", 30, int64Ptr(60), true),
	}
	view := ansi.Strip(m.View())
	sooner := strings.Index(view, "weekly usage [sooner]")
	later := strings.Index(view, "weekly usage [later]")
	unknown := strings.Index(view, "weekly usage [unknown]")
	if !(sooner >= 0 && sooner < later && later < unknown) {
		t.Fatalf("expected known weekly reset ordering then unknown:\n%s", view)
	}
}

func TestFixtureLayoutMatrixFitsViewportAndPinsExitHint(t *testing.T) {
	cases := []struct {
		name     string
		width    int
		height   int
		accounts int
	}{
		{name: "narrow", width: 36, height: 16, accounts: 1},
		{name: "standard", width: 80, height: 24, accounts: 2},
		{name: "wide", width: 132, height: 30, accounts: 3},
		{name: "short", width: 80, height: 12, accounts: 4},
		{name: "many accounts", width: 100, height: 26, accounts: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel(tc.width, tc.height, true)
			m.summary.WindowAccountLabel = ""
			m.summary.Accounts = make([]usage.AccountSummary, 0, tc.accounts)
			for i := 0; i < tc.accounts; i++ {
				reset := int64((i + 1) * 600)
				m.summary.Accounts = append(m.summary.Accounts, accountFixture(string(rune('a'+i)), 10+i, &reset, true))
			}
			view := ansi.Strip(m.View())
			t.Logf("%s fixture:\n%s", tc.name, view)
			assertViewport(t, view, tc.width, tc.height)
			lines := strings.Split(view, "\n")
			if !strings.Contains(lines[len(lines)-1], "Ctrl+C to exit") {
				t.Fatalf("expected exit hint pinned to bottom:\n%s", view)
			}
		})
	}
}

func TestLoadingAndErrorViewsFit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastError string
		want      string
	}{
		{name: "loading", want: "loading usage data"},
		{name: "error", lastError: "provider unavailable", want: "provider unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel(52, 10, true)
			m.summary = nil
			m.lastError = tc.lastError
			view := ansi.Strip(m.View())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("expected %q:\n%s", tc.want, view)
			}
			assertViewport(t, view, 52, 10)
		})
	}
}

func TestWeeklyObservedPanelShowsBreakdownAndDiagnostics(t *testing.T) {
	m := fixtureModel(100, 24, true)
	m.summary.Warnings = []string{"other warning", "account \"alpha\" fetch failed: auth expired"}
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"weekly token estimate [ready]", "- total: 12.3k", "- input: 8k",
		"- input (cached): 2k", "- output: 4.34k", "auth expired",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in lower panel:\n%s", want, view)
		}
	}
	if strings.Contains(view, "five-hour token estimate") {
		t.Fatalf("did not expect obsolete estimate status:\n%s", view)
	}
}

func TestWeeklyObservedPanelShowsLoadingPartialAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		warming  bool
		fetching bool
		want     string
	}{
		{name: "loading", status: "unavailable", fetching: true, want: "[loading]"},
		{name: "warming", status: "unavailable", warming: true, want: "[loading]"},
		{name: "partial", status: "partial", want: "[partial]"},
		{name: "unavailable", status: "unavailable", want: "[unavailable]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel(80, 20, true)
			m.fetching = tc.fetching
			m.summary.ObservedTokensStatus = tc.status
			m.summary.ObservedTokensWarming = tc.warming
			m.summary.ObservedTokensWeekly = nil
			m.summary.ObservedWindowWeekly = nil
			view := ansi.Strip(m.View())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("expected %q:\n%s", tc.want, view)
			}
		})
	}
}

func TestColorAndNoColorModesHaveSameText(t *testing.T) {
	color := fixtureModel(100, 22, false)
	plain := fixtureModel(100, 22, true)
	if ansi.Strip(color.View()) != ansi.Strip(plain.View()) {
		t.Fatalf("expected color and no-color modes to preserve the same text")
	}
	if color.styles.title.GetForeground() == plain.styles.title.GetForeground() {
		t.Fatalf("expected color mode to configure a distinct title color")
	}
}

func TestFetchResultKeepsLastGoodWeeklyCardsAsStale(t *testing.T) {
	m := fixtureModel(90, 22, true)
	m.lastGoodWindowData = cloneSummary(m.summary)
	current := &usage.Summary{
		WindowDataAvailable: false, TotalAccounts: 1, SuccessfulAccounts: 0,
		ObservedTokensStatus: "partial", Warnings: []string{"refresh failed"}, FetchedAt: m.now,
	}
	updated, _ := m.Update(fetchResultMsg{at: m.now, summary: current})
	got := updated.(Model)
	if !got.showingStaleWindows || got.summary.WeeklyWindow.UsedPercent != 35 {
		t.Fatalf("expected last good weekly snapshot, got %+v", got.summary)
	}
	view := ansi.Strip(got.View())
	if !strings.Contains(view, "weekly usage [alpha] [stale]") {
		t.Fatalf("expected stale weekly card:\n%s", view)
	}
}

func TestFetchResultKeepsLastGoodWeeklyCardsWhenFetchHasNoWeeklyWindow(t *testing.T) {
	m := fixtureModel(90, 22, true)
	m.lastGoodWindowData = cloneSummary(m.summary)
	current := &usage.Summary{
		WindowDataAvailable: false, TotalAccounts: 1, SuccessfulAccounts: 1,
		ObservedTokensStatus: "estimated", FetchedAt: m.now,
	}
	updated, _ := m.Update(fetchResultMsg{at: m.now, summary: current})
	got := updated.(Model)
	if !got.showingStaleWindows || got.summary.WeeklyWindow.UsedPercent != 35 {
		t.Fatalf("expected last good weekly snapshot after a weekly-less fetch, got %+v", got.summary)
	}
}

func TestHeaderAndFooterStayHumanFriendly(t *testing.T) {
	m := fixtureModel(80, 16, true)
	view := ansi.Strip(m.View())
	for _, want := range []string{"multicodex monitor", "[refresh <1m]", "local 2026-07-20 12:00", "Ctrl+C to exit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in header/footer:\n%s", want, view)
		}
	}
	if strings.Contains(view, "q to quit") {
		t.Fatalf("expected Ctrl+C to remain the only exit hint")
	}
}

func TestControlCQuits(t *testing.T) {
	m := fixtureModel(80, 16, true)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected Ctrl+C to return a quit command")
	}
}

func TestPreferredDiagnosticWarningOrder(t *testing.T) {
	warnings := []string{"active account usage unavailable", "account \"alpha\" fetch failed: timeout", "auth expired; sign in again"}
	if got := preferredDiagnosticWarning(warnings, "alpha"); got != warnings[2] {
		t.Fatalf("expected auth warning first, got %q", got)
	}
}

func TestWeeklyResetCountdownUsesLiveClockWhenExactResetIsKnown(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	reset := now.Add(30 * time.Minute)
	staleSeconds := int64((3 * time.Hour) / time.Second)
	m := Model{now: now}
	window := usage.WindowSummary{ResetsAt: &reset, SecondsUntilReset: &staleSeconds}

	if got := m.renderWindowResetRemaining(window); got != "30m" {
		t.Fatalf("expected live 30m countdown, got %q", got)
	}
	if got, known := m.windowResetSeconds(window); !known || got != int64((30*time.Minute)/time.Second) {
		t.Fatalf("expected live reset ordering value, got %d known=%v", got, known)
	}
	if got := m.renderExactResetTime(usage.WindowSummary{SecondsUntilReset: &staleSeconds}); got != "" {
		t.Fatalf("expected no drifting exact time for a relative-only reset, got %q", got)
	}
}

func fixtureModel(width, height int, noColor bool) Model {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2*time.Hour + 30*time.Minute)
	resetSeconds := int64((2*time.Hour + 30*time.Minute) / time.Second)
	sparkReset := now.Add(26 * time.Hour)
	sparkSeconds := int64(26 * time.Hour / time.Second)
	weekly := usage.WindowSummary{UsedPercent: 35, ResetsAt: &reset, SecondsUntilReset: &resetSeconds}
	spark := usage.WindowSummary{UsedPercent: 62, ResetsAt: &sparkReset, SecondsUntilReset: &sparkSeconds}
	total := int64(12345)
	summary := &usage.Summary{
		Source: "app-server", PlanType: "pro", WindowDataAvailable: true,
		WindowAccountLabel: "alpha", WeeklyWindow: weekly,
		RateLimitWindows: map[string]usage.RateLimitWindow{
			"codex":           {LimitID: "codex", WeeklyWindow: weekly},
			"codex_bengalfox": {LimitID: "codex_bengalfox", LimitName: "Spark", WeeklyWindow: spark},
		},
		TotalAccounts: 1, SuccessfulAccounts: 1,
		ObservedTokensWeekly: &total,
		ObservedWindowWeekly: &usage.ObservedTokenBreakdown{
			Total: 12345, Input: 8000, CachedInput: 2000, Output: 4345, ReasoningOutput: 1000, HasSplit: true,
		},
		ObservedTokensStatus: "estimated", FetchedAt: now,
	}
	m := NewModel(Options{
		NoColor: noColor, DisplayLocation: time.UTC,
		Fetch: func(context.Context) (*usage.Summary, error) { return nil, errors.New("unused") },
	})
	m.width, m.height, m.now = width, height, now
	m.fetching, m.summary, m.nextFetchAt = false, summary, now.Add(45*time.Second)
	return m
}

func accountFixture(label string, used int, reset *int64, fetched bool) usage.AccountSummary {
	window := usage.WindowSummary{UsedPercent: used, SecondsUntilReset: reset}
	var fetchedAt *time.Time
	if fetched {
		now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
		fetchedAt = &now
	}
	return usage.AccountSummary{
		Label: label, WeeklyWindow: window,
		RateLimitWindows: map[string]usage.RateLimitWindow{"codex": {LimitID: "codex", WeeklyWindow: window}},
		FetchedAt:        fetchedAt,
	}
}

func assertViewport(t *testing.T, view string, width, height int) {
	t.Helper()
	if got := lipgloss.Width(view); got > width {
		t.Fatalf("view width %d exceeds %d:\n%s", got, width, view)
	}
	if got := lipgloss.Height(view); got > height {
		t.Fatalf("view height %d exceeds %d:\n%s", got, height, view)
	}
}

func int64Ptr(value int64) *int64 { return &value }
