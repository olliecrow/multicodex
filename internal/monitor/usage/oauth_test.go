package usage

import "testing"

func TestBuildRateLimitWindowsFromOAuthAdditionalLimitsKeepsPrimaryOnlyLimit(t *testing.T) {
	windows := buildRateLimitWindowsFromOAuthAdditionalLimits([]oauthAdditionalRateLimit{
		{
			LimitName: "Spark",
			RateLimit: &oauthRateLimitDetails{
				PrimaryWindow: &oauthWindowSnapshot{
					UsedPercent:        42,
					LimitWindowSeconds: 5 * 60 * 60,
				},
			},
		},
	})

	window, ok := windows["Spark"]
	if !ok {
		t.Fatalf("expected primary-only additional limit to be preserved")
	}
	if window.Primary == nil || window.Primary.UsedPercent != 42 {
		t.Fatalf("expected primary usage 42, got %#v", window.Primary)
	}
	if window.Secondary != nil {
		t.Fatalf("expected missing secondary window to stay nil for normalizer fallback, got %#v", window.Secondary)
	}
}
