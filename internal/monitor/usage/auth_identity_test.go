package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemIdentityKeysPreserveCase(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "Case")
	lower := filepath.Join(root, "case")
	for _, home := range []string{upper, lower} {
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "CASE")); err == nil {
		t.Skip("temporary directory is on a case-insensitive filesystem")
	}

	if accountIdentityOrHomeKey(AccountSummary{}, upper) == accountIdentityOrHomeKey(AccountSummary{}, lower) {
		t.Fatal("case-distinct homes without auth files share a home identity key")
	}

	for _, home := range []string{upper, lower} {
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if authIdentityKeyForHome(upper) == authIdentityKeyForHome(lower) {
		t.Fatal("case-distinct auth files share an auth identity key")
	}

	alias := filepath.Join(root, "alias")
	if err := os.Symlink(upper, alias); err != nil {
		t.Fatal(err)
	}
	if authIdentityKeyForHome(alias) != authIdentityKeyForHome(upper) {
		t.Fatal("symlink alias to one home did not share its auth identity key")
	}
	if accountIdentityOrHomeKey(AccountSummary{}, alias) != accountIdentityOrHomeKey(AccountSummary{}, upper) {
		t.Fatal("symlink alias to one home did not share its identity key")
	}
}
