package usage

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOAuthSourceFetchAllowsPrimaryOnlyMainWindow(t *testing.T) {
	codexHome := t.TempDir()
	authJSON := `{"tokens":{"access_token":"test-token"}}`
	if err := os.WriteFile(codexHome+"/auth.json", []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	source := NewOAuthSourceForHome(codexHome)
	source.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token header, got %q", got)
		}
		body := `{
			"email": "user@example.com",
			"plan_type": "pro",
			"rate_limit": {
				"primary_window": {
					"used_percent": 12,
					"limit_window_seconds": 18000,
					"reset_at": 1893456000
				}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	summary, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if summary.PrimaryWindow.UsedPercent != 12 {
		t.Fatalf("expected primary usage 12, got %d", summary.PrimaryWindow.UsedPercent)
	}
	if summary.SecondaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected missing secondary to be unavailable, got %d", summary.SecondaryWindow.UsedPercent)
	}
	codexWindow, ok := summary.RateLimitWindows["codex"]
	if !ok {
		t.Fatalf("expected codex rate limit window")
	}
	if codexWindow.SecondaryWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected codex secondary to be unavailable, got %d", codexWindow.SecondaryWindow.UsedPercent)
	}
}

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
