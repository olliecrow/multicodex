package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	chatGPTOAuthUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage"
)

type OAuthSource struct {
	httpClient *http.Client
	codexHome  string
}

func NewOAuthSource() *OAuthSource {
	home, _ := defaultCodexHome()
	return NewOAuthSourceForHome(home)
}

func NewOAuthSourceForHome(codexHome string) *OAuthSource {
	return &OAuthSource{
		httpClient: &http.Client{Timeout: 8 * time.Second},
		codexHome:  strings.TrimSpace(codexHome),
	}
}

func (s *OAuthSource) Name() string {
	return "oauth"
}

func (s *OAuthSource) Fetch(ctx context.Context) (*Summary, error) {
	authPath, err := findAuthJSONPathForHome(s.codexHome)
	if err != nil {
		return nil, err
	}
	token, err := readAccessToken(authPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTOAuthUsageEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build oauth request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "multicodex-monitor/0.1")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth request failed: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1_000_000))
	if err != nil {
		return nil, fmt.Errorf("read oauth response: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth endpoint returned HTTP %d: %s", res.StatusCode, summarizeBody(body))
	}

	var payload oauthUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode oauth response: %w", err)
	}
	if payload.RateLimit == nil {
		return nil, errors.New("oauth response missing rate_limit")
	}
	if payload.RateLimit.PrimaryWindow == nil {
		return nil, errors.New("oauth response missing primary_window")
	}

	snapshot := rateLimitSnapshotRaw{
		LimitID:  "codex",
		PlanType: payload.PlanType,
		Primary: &rateLimitWindowRaw{
			UsedPercent:        payload.RateLimit.PrimaryWindow.UsedPercent,
			WindowDurationMins: toMins(payload.RateLimit.PrimaryWindow.LimitWindowSeconds),
			ResetsAt:           toInt64Ptr(payload.RateLimit.PrimaryWindow.ResetAt),
		},
	}
	if payload.RateLimit.SecondaryWindow != nil {
		snapshot.Secondary = &rateLimitWindowRaw{
			UsedPercent:        payload.RateLimit.SecondaryWindow.UsedPercent,
			WindowDurationMins: toMins(payload.RateLimit.SecondaryWindow.LimitWindowSeconds),
			ResetsAt:           toInt64Ptr(payload.RateLimit.SecondaryWindow.ResetAt),
		}
	}
	rateLimitsByLimitID := map[string]rateLimitSnapshotRaw{
		snapshot.LimitID: snapshot,
	}
	for id, window := range buildRateLimitWindowsFromOAuthAdditionalLimits(payload.AdditionalRateLimits) {
		rateLimitsByLimitID[id] = window
	}

	return normalizeSummary(
		s.Name(),
		snapshot,
		rateLimitsByLimitID,
		len(rateLimitsByLimitID)-1,
		&identityInfo{
			Email:     strings.TrimSpace(payload.Email),
			AccountID: strings.TrimSpace(payload.AccountID),
			UserID:    strings.TrimSpace(payload.UserID),
		},
		nil,
	)
}

func (s *OAuthSource) Close() error {
	return nil
}

type oauthUsagePayload struct {
	Email                string                     `json:"email"`
	AccountID            string                     `json:"account_id"`
	UserID               string                     `json:"user_id"`
	PlanType             string                     `json:"plan_type"`
	RateLimit            *oauthRateLimitDetails     `json:"rate_limit"`
	AdditionalRateLimits []oauthAdditionalRateLimit `json:"additional_rate_limits"`
}

type oauthAdditionalRateLimit struct {
	LimitName string                 `json:"limit_name"`
	RateLimit *oauthRateLimitDetails `json:"rate_limit"`
}

type oauthRateLimitDetails struct {
	Allowed         bool                 `json:"allowed"`
	LimitReached    bool                 `json:"limit_reached"`
	PrimaryWindow   *oauthWindowSnapshot `json:"primary_window"`
	SecondaryWindow *oauthWindowSnapshot `json:"secondary_window"`
}

type oauthWindowSnapshot struct {
	UsedPercent        int `json:"used_percent"`
	LimitWindowSeconds int `json:"limit_window_seconds"`
	ResetAfterSeconds  int `json:"reset_after_seconds"`
	ResetAt            int `json:"reset_at"`
}

type authFilePayload struct {
	Email  string `json:"email"`
	Tokens struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	} `json:"tokens"`
}

func findAuthJSONPath() (string, error) {
	home, err := defaultCodexHome()
	if err != nil {
		return "", err
	}
	return findAuthJSONPathForHome(home)
}

func findAuthJSONPathForHome(codexHome string) (string, error) {
	if strings.TrimSpace(codexHome) != "" {
		p := filepath.Join(codexHome, "auth.json")
		if fileExists(p) {
			return p, nil
		}
	}

	return "", fmt.Errorf("auth.json not found in %s", filepath.Join(codexHome, "auth.json"))
}

func readAccessToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read auth file: %w", err)
	}

	var payload authFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode auth file: %w", err)
	}
	token := strings.TrimSpace(payload.Tokens.AccessToken)
	if token == "" {
		return "", errors.New("auth.json missing tokens.access_token")
	}
	return token, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func summarizeBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 180 {
		return s[:180] + "..."
	}
	return s
}

func toMins(seconds int) *int {
	if seconds <= 0 {
		return nil
	}
	v := seconds / 60
	return &v
}

func toInt64Ptr(v int) *int64 {
	if v <= 0 {
		return nil
	}
	out := int64(v)
	return &out
}

func buildRateLimitWindowsFromOAuthAdditionalLimits(additionalLimits []oauthAdditionalRateLimit) map[string]rateLimitSnapshotRaw {
	if len(additionalLimits) == 0 {
		return nil
	}

	windowByLimit := map[string]rateLimitSnapshotRaw{}
	for i, additional := range additionalLimits {
		if additional.RateLimit == nil || additional.RateLimit.PrimaryWindow == nil {
			continue
		}

		limitName := strings.TrimSpace(additional.LimitName)
		if limitName == "" {
			limitName = "additional-" + strconv.Itoa(i)
		}
		windowByLimit[limitName] = rateLimitSnapshotRaw{
			LimitID:   limitName,
			LimitName: &additional.LimitName,
			Primary: &rateLimitWindowRaw{
				UsedPercent:        additional.RateLimit.PrimaryWindow.UsedPercent,
				WindowDurationMins: toMins(additional.RateLimit.PrimaryWindow.LimitWindowSeconds),
				ResetsAt:           toInt64Ptr(additional.RateLimit.PrimaryWindow.ResetAt),
			},
		}
		if additional.RateLimit.SecondaryWindow != nil {
			window := windowByLimit[limitName]
			window.Secondary = &rateLimitWindowRaw{
				UsedPercent:        additional.RateLimit.SecondaryWindow.UsedPercent,
				WindowDurationMins: toMins(additional.RateLimit.SecondaryWindow.LimitWindowSeconds),
				ResetsAt:           toInt64Ptr(additional.RateLimit.SecondaryWindow.ResetAt),
			}
			windowByLimit[limitName] = window
		}
	}
	if len(windowByLimit) == 0 {
		return nil
	}
	return windowByLimit
}
