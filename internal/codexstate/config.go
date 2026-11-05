package codexstate

import (
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// CredentialStoreFromTOML returns the root-level Codex credential store.
// Errors never quote config content, which can hold private values.
func CredentialStoreFromTOML(content string) (string, bool, error) {
	var document struct {
		CredentialStore any `toml:"cli_auth_credentials_store"`
	}
	if err := toml.Unmarshal([]byte(content), &document); err != nil {
		var decodeErr *toml.DecodeError
		if errors.As(err, &decodeErr) {
			row, column := decodeErr.Position()
			return "", false, fmt.Errorf("config is not valid TOML at line %d, column %d", row, column)
		}
		return "", false, errors.New("config is not valid TOML")
	}
	if document.CredentialStore == nil {
		return "", false, nil
	}
	store, ok := document.CredentialStore.(string)
	if !ok {
		return "", false, errors.New("cli_auth_credentials_store must be a string")
	}
	return store, true, nil
}
