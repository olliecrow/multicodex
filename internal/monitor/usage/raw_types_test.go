package usage

import "testing"

func TestNormalizeSummaryAllowsMissingSecondaryWindow(t *testing.T) {
	summary, err := normalizeSummary("oauth", rateLimitSnapshotRaw{
		PlanType: "free",
		Primary: &rateLimitWindowRaw{
			UsedPercent: 7,
		},
		Secondary: nil,
	}, 0, &identityInfo{Email: "crowoy1@gmail.com"}, nil)
	if err != nil {
		t.Fatalf("expected missing secondary window to be allowed, got error: %v", err)
	}
	if !summary.WindowDataAvailable {
		t.Fatalf("expected summary to remain available")
	}
	if summary.PrimaryWindow.UsedPercent != 7 {
		t.Fatalf("expected primary window to stay intact, got %d", summary.PrimaryWindow.UsedPercent)
	}
	if summary.SecondaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected secondary window sentinel, got %d", summary.SecondaryWindow.UsedPercent)
	}
	if summary.AccountEmail != "crowoy1@gmail.com" {
		t.Fatalf("expected identity email to be preserved, got %q", summary.AccountEmail)
	}
}
