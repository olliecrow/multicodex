package usage

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/olliecrow/multicodex/internal/buildinfo"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOAuthSourceFetchLeavesNonWeeklyPrimaryOnlyWindowUnknown(t *testing.T) {
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
		if got := req.Header.Get("User-Agent"); got != clientName+"/"+buildinfo.Version {
			t.Fatalf("expected versioned user agent, got %q", got)
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
	if summary.WeeklyWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected missing weekly window to be unavailable, got %d", summary.WeeklyWindow.UsedPercent)
	}
	codexWindow, ok := summary.RateLimitWindows["codex"]
	if !ok {
		t.Fatalf("expected codex rate limit window")
	}
	if codexWindow.WeeklyWindow.UsedPercent != unavailableUsedPercent {
		t.Fatalf("expected codex weekly window to be unavailable, got %d", codexWindow.WeeklyWindow.UsedPercent)
	}
}

func TestUsableAuthFileRejectsLoosePermissions(t *testing.T) {
	codexHome := t.TempDir()
	authPath := codexHome + "/auth.json"
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"test-token"}}`), 0o644); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	ok, err := usableAuthFile(authPath)
	if err == nil {
		t.Fatal("expected loose auth permissions to fail")
	}
	if ok {
		t.Fatal("expected loose auth file not to be usable")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected permissions error, got %v", err)
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
