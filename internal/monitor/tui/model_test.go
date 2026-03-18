package tui

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"multicodex/internal/monitor/usage"
)

func TestViewFitsViewportAcrossSizes(t *testing.T) {
	sizes := []struct {
		width  int
		height int
	}{
		{60, 18},
		{80, 22},
		{100, 26},
		{140, 34},
	}

	for _, s := range sizes {
		t.Run(strconv.Itoa(s.width)+"x"+strconv.Itoa(s.height), func(t *testing.T) {
			m := seededModel()
			m.width = s.width
			m.height = s.height
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != s.height {
				t.Fatalf("expected %d lines, got %d", s.height, len(lines))
			}
			for i, line := range lines {
				if lipgloss.Width(line) > s.width {
					t.Fatalf("line %d exceeded width: got %d max %d", i+1, lipgloss.Width(line), s.width)
				}
			}
		})
	}
}

func TestMultiAccountViewFitsViewportAcrossSizes(t *testing.T) {
	sizes := []struct {
		width  int
		height int
	}{
		{60, 18},
		{80, 24},
		{100, 28},
		{140, 40},
	}

	for _, s := range sizes {
		t.Run(strconv.Itoa(s.width)+"x"+strconv.Itoa(s.height), func(t *testing.T) {
			m := seededMultiAccountModel()
			m.width = s.width
			m.height = s.height
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != s.height {
				t.Fatalf("expected %d lines, got %d", s.height, len(lines))
			}
			for i, line := range lines {
				if lipgloss.Width(line) > s.width {
					t.Fatalf("line %d exceeded width: got %d max %d", i+1, lipgloss.Width(line), s.width)
				}
			}
		})
	}
}

func TestNarrowViewStillRendersCoreFields(t *testing.T) {
	m := seededModel()
	m.width = 42
	m.height = 14
	out := m.View()
	t.Log(out)
	if !strings.Contains(out, "five-hour window") {
		t.Fatalf("expected five-hour section in output")
	}
	if !strings.Contains(out, "weekly window") {
		t.Fatalf("expected weekly section in output")
	}
}

func TestViewRendersAggregatedTokenSection(t *testing.T) {
	m := seededModel()
	m.width = 140
	m.height = 40
	total5h := int64(120000)
	total1w := int64(450000)
	m.summary.ObservedTokensStatus = "estimated"
	m.summary.ObservedTokens5h = &total5h
	m.summary.ObservedTokensWeekly = &total1w
	m.summary.ObservedWindow5h = &usage.ObservedTokenBreakdown{
		Total:       total5h,
		Input:       100000,
		CachedInput: 90000,
		Output:      20000,
		HasSplit:    true,
	}
	m.summary.ObservedWindowWeekly = &usage.ObservedTokenBreakdown{
		Total:       total1w,
		Input:       300000,
		CachedInput: 200000,
		Output:      150000,
		HasSplit:    true,
	}
	out := m.View()
	if !strings.Contains(out, "five-hour tokens [ready] (sum across accounts):") {
		t.Fatalf("expected aggregated five-hour token line in output")
	}
	if !strings.Contains(out, "weekly tokens [ready] (sum across accounts):") {
		t.Fatalf("expected aggregated weekly token line in output")
	}
	if !strings.Contains(out, "- total: 120k") {
		t.Fatalf("expected five-hour total bullet line")
	}
	if !strings.Contains(out, "- input: 100k") {
		t.Fatalf("expected five-hour input bullet line")
	}
	if !strings.Contains(out, "- output (reasoning): 0") {
		t.Fatalf("expected five-hour output(reasoning) bullet line")
	}
	if !strings.Contains(out, "- input (cached): 90k") {
		t.Fatalf("expected five-hour input(cached) bullet line")
	}
}

func TestWindowTitlesShowAccountNameAndMetaOmitsCurrentAccountLine(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 30
	out := m.View()
	if !strings.Contains(out, "five-hour window [me]") {
		t.Fatalf("expected five-hour title to include account name")
	}
	if !strings.Contains(out, "weekly window [me]") {
		t.Fatalf("expected weekly title to include account name")
	}
	if strings.Contains(out, "current account:") {
		t.Fatalf("did not expect current account line in bottom metadata panel")
	}
}

func TestWindowPanelsShowUnavailableWhenActiveWindowDataMissing(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 28
	m.summary.WindowDataAvailable = false
	m.summary.AccountEmail = ""
	out := m.View()
	if !strings.Contains(out, "five-hour window [me]") {
		t.Fatalf("expected active account name in five-hour title")
	}
	if !strings.Contains(out, "used: unavailable") {
		t.Fatalf("expected unavailable used value")
	}
}

func TestSingleAccountViewDoesNotRenderExtraAccountWindowRows(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 30
	now := m.now
	m.summary.TotalAccounts = 1
	m.summary.Accounts = []usage.AccountSummary{
		{
			Label:        "me",
			AccountEmail: "me@example.com",
			FetchedAt:    &now,
		},
	}

	out := m.View()
	if strings.Count(out, "five-hour window [") != 1 {
		t.Fatalf("expected one five-hour window row, got:\n%s", out)
	}
	if strings.Count(out, "weekly window [") != 1 {
		t.Fatalf("expected one weekly window row, got:\n%s", out)
	}
}

func TestMultiAccountViewRendersOneWindowRowPerAccount(t *testing.T) {
	m := seededMultiAccountModel()
	m.width = 120
	m.height = 40

	out := m.View()
	if strings.Count(out, "five-hour window [") != 3 {
		t.Fatalf("expected one five-hour row per account, got:\n%s", out)
	}
	if strings.Count(out, "weekly window [") != 3 {
		t.Fatalf("expected one weekly row per account, got:\n%s", out)
	}
	if strings.Index(out, "five-hour window [me]") >= strings.Index(out, "five-hour window [alpha]") {
		t.Fatalf("expected active account row to stay above additional account rows")
	}
}

func TestMultiAccountViewDoesNotDuplicateFailedActiveAccountRow(t *testing.T) {
	m := seededMultiAccountModel()
	m.width = 120
	m.height = 40
	m.summary.WindowDataAvailable = false
	m.summary.AccountEmail = ""
	m.summary.WindowAccountLabel = "me"

	out := m.View()
	if strings.Count(out, "five-hour window [") != 3 {
		t.Fatalf("expected unavailable active row plus two non-active rows, got:\n%s", out)
	}
	if strings.Count(out, "five-hour window [me]") != 1 {
		t.Fatalf("expected failed active account to appear exactly once, got:\n%s", out)
	}
}

func TestMultiAccountShortViewportKeepsAggregatePanelVisible(t *testing.T) {
	m := seededMultiAccountModel()
	m.width = 120
	m.height = 24

	out := m.View()
	if !strings.Contains(out, "accounts: 3 detected") {
		t.Fatalf("expected aggregate bottom panel to remain visible, got:\n%s", out)
	}
	if !strings.Contains(out, "weekly tokens [") {
		t.Fatalf("expected weekly aggregate section to remain visible, got:\n%s", out)
	}
	if strings.Contains(out, "five-hour window [bravo]") {
		t.Fatalf("expected extra account rows to yield before the aggregate panel in a short viewport")
	}
}

func TestAccountsLineShowsDetectedAndNames(t *testing.T) {
	m := seededModel()
	m.width = 140
	m.height = 28
	m.summary.TotalAccounts = 3
	m.summary.Accounts = []usage.AccountSummary{
		{Label: "alpha", AccountEmail: "a@example.com"},
		{Label: "bravo", AccountID: "acc-123"},
		{},
	}

	out := m.View()
	if !strings.Contains(out, "accounts: 3 detected [alpha, bravo, unidentified]") {
		t.Fatalf("expected detected-account name list in accounts line, got:\n%s", out)
	}
}

func TestAccountsLineShowsDistinctLabelsForUnidentifiedAccounts(t *testing.T) {
	m := seededModel()
	m.width = 140
	m.height = 28
	m.summary.TotalAccounts = 2
	m.summary.Accounts = []usage.AccountSummary{
		{Label: "apple"},
		{Label: "crowoy"},
	}

	out := m.View()
	if !strings.Contains(out, "accounts: 2 detected [apple, crowoy]") {
		t.Fatalf("expected unidentified accounts to remain distinct by label, got:\n%s", out)
	}
}

func TestAccountsLineTruncatesWithDots(t *testing.T) {
	m := seededModel()
	m.summary.TotalAccounts = 2
	m.summary.Accounts = []usage.AccountSummary{
		{Label: "very-long-first-account-name"},
		{Label: "very-long-second-account-name"},
	}

	line := m.renderAccountsLine(40)
	if !strings.Contains(line, "accounts: ") {
		t.Fatalf("expected accounts line")
	}
	if !strings.Contains(line, "...") {
		t.Fatalf("expected accounts line truncation with three dots")
	}
}

func TestTokenBreakdownLinesStayFixedWithNAPlaceholders(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 26
	m.summary.ObservedWindow5h = nil
	m.summary.ObservedTokens5h = nil
	m.summary.ObservedWindowWeekly = nil
	m.summary.ObservedTokensWeekly = nil

	out := m.View()
	for _, expected := range []string{
		"- total: n/a",
		"- input: n/a",
		"- input (cached): n/a",
		"- output: n/a",
		"- output (reasoning): n/a",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected fixed placeholder token line %q", expected)
		}
	}
}

func TestStatusSectionFixedRowsAcrossCounts(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 30
	m.summary.Warnings = nil
	base := m.View()
	baseStatusLines := countStatusRows(base)
	if baseStatusLines < 1 {
		t.Fatalf("expected status lines in output")
	}

	m.summary.Warnings = []string{
		"first warning",
		"second warning",
		"third warning",
		"fourth warning",
		"fifth warning",
	}
	withWarnings := m.View()
	withStatusLines := countStatusRows(withWarnings)
	if withStatusLines != baseStatusLines {
		t.Fatalf("expected status section row count to remain fixed")
	}
	if !strings.Contains(withWarnings, "status [active windows]:") {
		t.Fatalf("expected descriptive status check labels")
	}
	if strings.Contains(withWarnings, "warning [more checks]:") {
		t.Fatalf("did not expect hidden-check overflow for roomy viewport")
	}
}

func TestStatusRowsForLayoutExpandsInTallViewport(t *testing.T) {
	rows := statusRowsForLayout(46, 6, 2)
	if rows <= 4 {
		t.Fatalf("expected status rows to expand beyond visible checks in tall viewport, got %d", rows)
	}
}

func TestObservedHeaderShowsLoadingWhenUnavailableAndFetching(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 24
	m.fetching = true
	m.summary.ObservedWindow5h = nil
	m.summary.ObservedTokens5h = nil
	out := m.View()
	if !strings.Contains(out, "five-hour tokens [loading") {
		t.Fatalf("expected loading state in five-hour token header when unavailable and fetching")
	}
	if strings.Contains(out, "[loading -") || strings.Contains(out, "[refreshing -") {
		t.Fatalf("did not expect spinner dash in loading/refreshing state")
	}
}

func TestObservedHeaderShowsLoadingWhenWarmingWithoutFetchInFlight(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 24
	m.fetching = false
	m.summary.ObservedWindow5h = nil
	m.summary.ObservedTokens5h = nil
	m.summary.ObservedTokensStatus = "unavailable"
	m.summary.ObservedTokensWarming = true

	out := m.View()
	if !strings.Contains(out, "five-hour tokens [loading] (sum across accounts):") {
		t.Fatalf("expected loading state from explicit warming flag")
	}
}

func TestHeaderShowsCtrlCOnly(t *testing.T) {
	m := seededModel()
	m.width = 100
	m.height = 20
	header := m.renderHeader()
	if strings.Contains(header, "ctrl+c") || strings.Contains(header, "q quit") || strings.Contains(header, "r refresh") {
		t.Fatalf("expected header without interactive key hints, got: %q", header)
	}
}

func TestWindowPanelCompactsResetLineIntoBracketedCountdown(t *testing.T) {
	m := seededModel()
	m.width = 100
	m.height = 20
	out := m.renderBody()
	if !strings.Contains(out, "resets at: 2026-02-26 11:30 [1h30m]") {
		t.Fatalf("expected compact reset line in window panel")
	}
	if strings.Contains(out, "resets in:") {
		t.Fatalf("did not expect separate resets in row in window panels")
	}
}

func TestWideLayoutPanelsAlignWidths(t *testing.T) {
	widths := []int{98, 99, 100, 101, 120, 121, 140}
	heights := []int{18, 24, 32}

	for _, w := range widths {
		for _, h := range heights {
			m := seededModel()
			m.width = w
			m.height = h
			body := m.renderBody()

			lines := strings.Split(body, "\n")
			topLine := ""
			metaTop := ""
			for _, line := range lines {
				if strings.Count(line, "╭") >= 2 && topLine == "" {
					topLine = line
					continue
				}
				if strings.Count(line, "╭") == 1 {
					metaTop = line
				}
			}
			if topLine == "" || metaTop == "" {
				t.Fatalf("expected top and metadata panel border lines for %dx%d", w, h)
			}
			topWidth := lipgloss.Width(topLine)
			metaWidth := lipgloss.Width(metaTop)
			if topWidth != metaWidth {
				t.Fatalf("expected aligned widths for %dx%d, got top=%d meta=%d", w, h, topWidth, metaWidth)
			}

			topRunes := []rune(topLine)
			firstRight := nthRuneIndex(topRunes, '╮', 1)
			secondLeft := nthRuneIndex(topRunes, '╭', 2)
			if firstRight < 0 || secondLeft < 0 || secondLeft <= firstRight {
				t.Fatalf("expected two top panels for %dx%d", w, h)
			}
			gapStart := firstRight + 1
			gapEnd := secondLeft - 1
			dividerCenter := (float64(gapStart) + float64(gapEnd)) / 2.0
			fullCenter := float64(topWidth-1) / 2.0
			if math.Abs(dividerCenter-fullCenter) > 0.5 {
				t.Fatalf("expected centered divider for %dx%d, divider=%.1f full=%.1f", w, h, dividerCenter, fullCenter)
			}
		}
	}
}

func TestHeaderIncludesRefreshBracketOnTopLine(t *testing.T) {
	m := seededModel()
	m.width = 100
	header := m.renderHeader()
	lines := strings.Split(header, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected single-line header")
	}
	if !strings.Contains(lines[0], "[next refresh in ") {
		t.Fatalf("expected bracketed refresh countdown on header line")
	}
	if strings.Contains(lines[0], "interval") {
		t.Fatalf("did not expect interval label in header")
	}
	if lipgloss.Width(lines[0]) > m.width {
		t.Fatalf("header line exceeded width")
	}
}

func TestHeaderShowsLocalTimestampWithoutSecondsAtNarrowWidth(t *testing.T) {
	m := seededModel()
	m.width = 58
	header := m.renderHeader()
	lines := strings.Split(header, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected single-line header")
	}
	if !strings.Contains(lines[0], "local 2026-02-26 10:00") {
		t.Fatalf("expected narrow header to show local timestamp, got: %q", lines[0])
	}
	if strings.Contains(lines[0], ":00:") {
		t.Fatalf("did not expect seconds in header timestamp, got: %q", lines[0])
	}
	if lipgloss.Width(lines[0]) > m.width {
		t.Fatalf("header line exceeded width")
	}
}

func TestViewportClippingHasNoEllipsisArtifacts(t *testing.T) {
	m := seededModel()
	m.width = 95
	m.height = 22
	out := m.View()
	if strings.Contains(out, "…") {
		t.Fatalf("expected no ellipsis clipping artifacts in viewport output")
	}
}

func TestViewShowsExitHintAtBottom(t *testing.T) {
	m := seededModel()
	m.width = 120
	m.height = 30
	out := m.View()
	if !strings.Contains(out, "Ctrl+C to exit") {
		t.Fatalf("expected bottom exit hint in view")
	}
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("expected %d lines, got %d", m.height, len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "Ctrl+C to exit") {
		t.Fatalf("expected exit hint on bottom row, got: %q", lines[len(lines)-1])
	}
	if strings.Contains(out, "last successful snapshot") {
		t.Fatalf("did not expect last successful snapshot footer line")
	}
}

func nthRuneIndex(runes []rune, target rune, n int) int {
	if n <= 0 {
		return -1
	}
	count := 0
	for i, r := range runes {
		if r == target {
			count++
			if count == n {
				return i
			}
		}
	}
	return -1
}

func countStatusRows(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "warning [") || strings.Contains(line, "status [") || strings.Contains(line, "error [") {
			count++
		}
	}
	return count
}

func seededModel() Model {
	now := time.Date(2026, 2, 26, 15, 0, 0, 0, time.UTC)
	reset1 := now.Add(90 * time.Minute)
	reset2 := now.Add(7 * 24 * time.Hour)
	sec1 := int64(90 * 60)
	sec2 := int64(7 * 24 * 60 * 60)
	m := NewModel(Options{
		Interval:        15 * time.Second,
		Timeout:         8 * time.Second,
		NoColor:         true,
		DisplayLocation: time.FixedZone("test-local", -5*60*60),
		Fetch: func(_ context.Context) (*usage.Summary, error) {
			return nil, nil
		},
	})
	m.now = now
	m.fetching = false
	m.lastAttemptAt = now.Add(-2 * time.Second)
	m.lastSuccessAt = now.Add(-2 * time.Second)
	m.lastFetchDuration = 420 * time.Millisecond
	m.nextFetchAt = now.Add(13 * time.Second)
	m.summary = &usage.Summary{
		Source:              "app-server",
		PlanType:            "pro",
		AccountEmail:        "me@example.com",
		WindowAccountLabel:  "me",
		WindowDataAvailable: true,
		PrimaryWindow: usage.WindowSummary{
			UsedPercent:       41,
			ResetsAt:          &reset1,
			SecondsUntilReset: &sec1,
		},
		SecondaryWindow: usage.WindowSummary{
			UsedPercent:       69,
			ResetsAt:          &reset2,
			SecondsUntilReset: &sec2,
		},
		FetchedAt: now.Add(-2 * time.Second),
	}
	return m
}

func seededMultiAccountModel() Model {
	m := seededModel()
	now := m.now.Add(-2 * time.Second)
	alphaReset1 := m.now.Add(2 * time.Hour)
	alphaReset2 := m.now.Add(5 * 24 * time.Hour)
	alphaSec1 := int64(2 * 60 * 60)
	alphaSec2 := int64(5 * 24 * 60 * 60)
	bravoReset1 := m.now.Add(30 * time.Minute)
	bravoReset2 := m.now.Add(3 * 24 * time.Hour)
	bravoSec1 := int64(30 * 60)
	bravoSec2 := int64(3 * 24 * 60 * 60)

	m.summary.TotalAccounts = 3
	m.summary.WindowAccountLabel = "me"
	m.summary.Accounts = []usage.AccountSummary{
		{
			Label:        "alpha",
			AccountEmail: "alpha@example.com",
			PrimaryWindow: usage.WindowSummary{
				UsedPercent:       12,
				ResetsAt:          &alphaReset1,
				SecondsUntilReset: &alphaSec1,
			},
			SecondaryWindow: usage.WindowSummary{
				UsedPercent:       28,
				ResetsAt:          &alphaReset2,
				SecondsUntilReset: &alphaSec2,
			},
			FetchedAt: &now,
		},
		{
			Label:        "bravo",
			AccountEmail: "bravo@example.com",
			PrimaryWindow: usage.WindowSummary{
				UsedPercent:       77,
				ResetsAt:          &bravoReset1,
				SecondsUntilReset: &bravoSec1,
			},
			SecondaryWindow: usage.WindowSummary{
				UsedPercent:       84,
				ResetsAt:          &bravoReset2,
				SecondsUntilReset: &bravoSec2,
			},
			FetchedAt: &now,
		},
		{
			Label:           "me",
			AccountEmail:    "me@example.com",
			PrimaryWindow:   m.summary.PrimaryWindow,
			SecondaryWindow: m.summary.SecondaryWindow,
			FetchedAt:       &now,
		},
	}
	return m
}
