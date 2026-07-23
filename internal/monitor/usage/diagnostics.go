package usage

import (
	"fmt"
	"strings"
)

func safeProviderHTTPError(source string, status int, body []byte) error {
	message := fmt.Sprintf("%s returned HTTP %d", source, status)
	if detail := providerAuthFailure(string(body)); detail != "" {
		message += ": " + detail
	}
	return fmt.Errorf("%s", message)
}

func safeProviderRPCError(method string, rpcErr *rpcError) error {
	message := fmt.Sprintf("%s failed with RPC code %d", method, rpcErr.Code)
	if detail := providerAuthFailure(rpcErr.Message); detail != "" {
		message += ": " + detail
	}
	return fmt.Errorf("%s", message)
}

func providerAuthFailure(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "token_expired"),
		strings.Contains(lower, "authentication token is expired"),
		strings.Contains(lower, "auth token is expired"):
		return "authentication expired; sign in again"
	case strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "authentication rejected"),
		strings.Contains(lower, "invalid token"):
		return "authentication rejected; sign in again"
	default:
		return ""
	}
}
