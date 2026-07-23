package codexstate

import "testing"

func TestCredentialStoreFromTOML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantStore string
		wantFound bool
		wantError bool
	}{
		{name: "root basic string", content: "cli_auth_credentials_store = \"file\"\n", wantStore: "file", wantFound: true},
		{name: "root literal string", content: "cli_auth_credentials_store = 'file'\n", wantStore: "file", wantFound: true},
		{name: "escaped basic string", content: `cli_auth_credentials_store = "f\u0069le"`, wantStore: "file", wantFound: true},
		{name: "quoted equals before key", content: "\"not=the_key\" = \"x\"\ncli_auth_credentials_store = \"file\"\n", wantStore: "file", wantFound: true},
		{name: "commented key", content: "# cli_auth_credentials_store = \"file\"\n", wantFound: false},
		{name: "nested key", content: "[auth]\ncli_auth_credentials_store = \"file\"\n", wantFound: false},
		{name: "lookalike key", content: "cli_auth_credentials_store_backup = \"file\"\n", wantFound: false},
		{name: "invalid value", content: "cli_auth_credentials_store = file\n", wantError: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, found, err := CredentialStoreFromTOML(test.content)
			if (err != nil) != test.wantError {
				t.Fatalf("CredentialStoreFromTOML() error = %v, wantError %v", err, test.wantError)
			}
			if store != test.wantStore || found != test.wantFound {
				t.Fatalf("CredentialStoreFromTOML() = (%q, %v), want (%q, %v)", store, found, test.wantStore, test.wantFound)
			}
		})
	}
}
