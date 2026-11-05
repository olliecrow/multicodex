package codexstate

import (
	"strings"
	"testing"
)

func TestCredentialStoreFromTOML(t *testing.T) {
	t.Parallel()

	const marker = "SYNTHETIC-PRIVATE-MARKER"
	tests := []struct {
		name      string
		content   string
		wantStore string
		wantFound bool
		wantError bool
	}{
		{name: "root basic string", content: "cli_auth_credentials_store = \"file\"\n", wantStore: "file", wantFound: true},
		{name: "root literal string", content: "cli_auth_credentials_store = 'file'\n", wantStore: "file", wantFound: true},
		{name: "multiline basic string", content: "cli_auth_credentials_store = \"\"\"file\"\"\"\n", wantStore: "file", wantFound: true},
		{name: "multiline literal string", content: "cli_auth_credentials_store = '''file'''\n", wantStore: "file", wantFound: true},
		{name: "quoted key", content: "\"cli_auth_credentials_store\" = \"keyring\"\n", wantStore: "keyring", wantFound: true},
		{name: "escaped basic string", content: `cli_auth_credentials_store = "file"`, wantStore: "file", wantFound: true},
		{name: "trailing comment", content: "cli_auth_credentials_store = \"file\" # keep\n", wantStore: "file", wantFound: true},
		{name: "quoted equals before key", content: "\"not=the_key\" = \"x\"\ncli_auth_credentials_store = \"file\"\n", wantStore: "file", wantFound: true},
		{name: "after multiline value", content: "notes = \"\"\"\ncli_auth_credentials_store = \"keyring\"\n\"\"\"\ncli_auth_credentials_store = \"file\"\n", wantStore: "file", wantFound: true},
		{name: "commented key", content: "# cli_auth_credentials_store = \"file\"\n", wantFound: false},
		{name: "nested key", content: "[auth]\ncli_auth_credentials_store = \"file\"\n", wantFound: false},
		{name: "dotted nested key", content: "auth.cli_auth_credentials_store = \"file\"\n", wantFound: false},
		{name: "lookalike key", content: "cli_auth_credentials_store_backup = \"file\"\n", wantFound: false},
		{name: "unrelated keys", content: "model = \"x\"\n[mcp_servers.demo]\ncommand = \"demo\"\n", wantFound: false},
		{name: "empty", content: "", wantFound: false},
		{name: "duplicate key", content: "cli_auth_credentials_store = \"file\"\ncli_auth_credentials_store = \"keyring\"\n", wantError: true},
		{name: "bare value", content: "cli_auth_credentials_store = " + marker + "\n", wantError: true},
		{name: "wrong type", content: "cli_auth_credentials_store = 1\n", wantError: true},
		{name: "malformed unrelated syntax", content: "token = \"" + marker + "\ncli_auth_credentials_store = \"file\"\n", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, found, err := CredentialStoreFromTOML(test.content)
			if (err != nil) != test.wantError {
				t.Fatalf("CredentialStoreFromTOML() error = %v, wantError %v", err, test.wantError)
			}
			if err != nil && strings.Contains(err.Error(), marker) {
				t.Fatalf("parse error quoted config content: %v", err)
			}
			if store != test.wantStore || found != test.wantFound {
				t.Fatalf("CredentialStoreFromTOML() = (%q, %v), want (%q, %v)", store, found, test.wantStore, test.wantFound)
			}
		})
	}
}
