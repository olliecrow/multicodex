package usage

import (
	"strings"
	"testing"
)

func TestSafeProviderHTTPErrorDoesNotExposeBody(t *testing.T) {
	err := safeProviderHTTPError("oauth endpoint", 500, []byte(`{"secret":"synthetic-secret-value"}`))
	if strings.Contains(err.Error(), "synthetic-secret-value") {
		t.Fatalf("provider body leaked into error: %v", err)
	}
	if err.Error() != "oauth endpoint returned HTTP 500" {
		t.Fatalf("unexpected safe error: %v", err)
	}
}

func TestSafeProviderErrorsPreserveAuthRecoveryGuidance(t *testing.T) {
	httpErr := safeProviderHTTPError("oauth endpoint", 401, []byte(`{"code":"token_expired","message":"synthetic-secret-value"}`))
	if got := httpErr.Error(); got != "oauth endpoint returned HTTP 401: authentication expired; sign in again" {
		t.Fatalf("unexpected HTTP auth error: %q", got)
	}

	rpcErr := safeProviderRPCError("account/read", &rpcError{
		Code:    -32000,
		Message: "Unauthorized synthetic-secret-value",
	})
	if strings.Contains(rpcErr.Error(), "synthetic-secret-value") {
		t.Fatalf("RPC message leaked into error: %v", rpcErr)
	}
	if got := rpcErr.Error(); got != "account/read failed with RPC code -32000: authentication rejected; sign in again" {
		t.Fatalf("unexpected RPC auth error: %q", got)
	}
}
