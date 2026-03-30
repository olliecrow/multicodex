package usage

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func accountEmailFromAuthFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var payload authFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if email := strings.TrimSpace(payload.Email); email != "" {
		return email, nil
	}
	if strings.TrimSpace(payload.Tokens.IDToken) == "" {
		return "", nil
	}

	parts := strings.Split(payload.Tokens.IDToken, ".")
	if len(parts) < 2 {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", err
	}
	return strings.TrimSpace(claims.Email), nil
}

func accountEmailFromAuthFileForHome(codexHome string) (string, error) {
	if home := normalizeHome(codexHome); home != "" {
		return accountEmailFromAuthFile(filepath.Join(home, "auth.json"))
	}
	return "", nil
}

func authIdentityKeyForHome(codexHome string) string {
	home := normalizeHome(codexHome)
	if home == "" {
		return ""
	}
	authPath := filepath.Join(home, "auth.json")
	resolved, err := filepath.EvalSymlinks(authPath)
	if err != nil {
		return ""
	}
	resolved = filepath.Clean(strings.TrimSpace(resolved))
	if resolved == "" {
		return ""
	}
	return "auth_file:" + strings.ToLower(resolved)
}
