package usage

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

func RunDoctor(ctx context.Context) DoctorReport {
	var checks []DoctorCheck

	checks = append(checks, checkCodexBinary(ctx))
	checks = append(checks, checkAuthJSON())

	appSource := NewAppServerSource()
	defer appSource.Close()
	checks = append(checks, checkSourceFetch(ctx, appSource, 8*time.Second))

	oauthSource := NewOAuthSource()
	defer oauthSource.Close()
	checks = append(checks, checkSourceFetch(ctx, oauthSource, 8*time.Second))

	return DoctorReport{Checks: checks}
}

func (r DoctorReport) Healthy() bool {
	var appOK, oauthOK bool
	for _, c := range r.Checks {
		switch c.Name {
		case "app-server fetch":
			appOK = c.OK
		case "oauth fetch":
			oauthOK = c.OK
		}
	}
	return appOK || oauthOK
}

func checkCodexBinary(ctx context.Context) DoctorCheck {
	cmd := exec.CommandContext(ctx, "codex", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DoctorCheck{
			Name:    "codex binary",
			OK:      false,
			Details: fmt.Sprintf("failed to execute codex --version: %v", err),
		}
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		version = "version output is empty"
	}
	return DoctorCheck{
		Name:    "codex binary",
		OK:      true,
		Details: version,
	}
}

func checkAuthJSON() DoctorCheck {
	path, err := findAuthJSONPath()
	if err != nil {
		return DoctorCheck{
			Name:    "auth file",
			OK:      false,
			Details: err.Error(),
		}
	}
	if _, err := readAccessToken(path); err != nil {
		return DoctorCheck{
			Name:    "auth file",
			OK:      false,
			Details: fmt.Sprintf("found %s but token read failed: %v", path, err),
		}
	}
	return DoctorCheck{
		Name:    "auth file",
		OK:      true,
		Details: fmt.Sprintf("found %s with access token", path),
	}
}

func checkSourceFetch(parent context.Context, source Source, timeout time.Duration) DoctorCheck {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	summary, err := source.Fetch(ctx)
	if err != nil {
		return DoctorCheck{
			Name:    source.Name() + " fetch",
			OK:      false,
			Details: err.Error(),
		}
	}
	return DoctorCheck{
		Name: source.Name() + " fetch",
		OK:   true,
		Details: fmt.Sprintf(
			"plan=%s 5h=%d%% weekly=%d%% source=%s",
			summary.PlanType,
			summary.PrimaryWindow.UsedPercent,
			summary.SecondaryWindow.UsedPercent,
			summary.Source,
		),
	}
}
