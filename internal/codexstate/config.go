package codexstate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const credentialStoreKey = "cli_auth_credentials_store"

// CredentialStoreFromTOML returns the root-level Codex credential store.
func CredentialStoreFromTOML(content string) (string, bool, error) {
	inRootTable := true
	multilineDelimiter := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
			continue
		}
		if multilineDelimiter != "" {
			if strings.Contains(line, multilineDelimiter) {
				multilineDelimiter = ""
			}
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			inRootTable = false
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inRootTable = false
			continue
		}
		if !inRootTable {
			continue
		}

		assignIndex := indexTOMLUnquotedByte(line, '=')
		if assignIndex == -1 {
			continue
		}

		key, err := parseTOMLKey(line[:assignIndex])
		if err != nil {
			return "", false, err
		}
		if key != credentialStoreKey {
			value := strings.TrimSpace(line[assignIndex+1:])
			switch {
			case strings.HasPrefix(value, `"""`) && strings.Count(value, `"""`)%2 == 1:
				multilineDelimiter = `"""`
			case strings.HasPrefix(value, `'''`) && strings.Count(value, `'''`)%2 == 1:
				multilineDelimiter = `'''`
			}
			continue
		}

		value, err := parseTOMLStringValue(line[assignIndex+1:])
		if err != nil {
			return "", false, err
		}
		return value, true, nil
	}
	return "", false, nil
}

func parseTOMLKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errors.New("empty config key")
	}
	if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
		unquoted, err := strconv.Unquote(key)
		if err != nil {
			return "", fmt.Errorf("invalid quoted config key %q: %w", key, err)
		}
		return unquoted, nil
	}
	if len(key) >= 2 && key[0] == '\'' && key[len(key)-1] == '\'' {
		return key[1 : len(key)-1], nil
	}
	return key, nil
}

func parseTOMLStringValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("cli_auth_credentials_store has empty value")
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted cli_auth_credentials_store value %q: %w", value, err)
		}
		return unquoted, nil
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], nil
	}
	return "", fmt.Errorf("invalid cli_auth_credentials_store value %q", value)
}

func stripTOMLComment(line string) string {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(line); i++ {
		character := line[i]
		switch {
		case escaped:
			escaped = false
		case inDouble:
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inDouble = false
			}
		case inSingle:
			if character == '\'' {
				inSingle = false
			}
		default:
			switch character {
			case '"':
				inDouble = true
			case '\'':
				inSingle = true
			case '#':
				return line[:i]
			}
		}
	}
	return line
}

func indexTOMLUnquotedByte(value string, needle byte) int {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(value); i++ {
		character := value[i]
		switch {
		case escaped:
			escaped = false
		case inDouble:
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inDouble = false
			}
		case inSingle:
			if character == '\'' {
				inSingle = false
			}
		default:
			switch character {
			case '"':
				inDouble = true
			case '\'':
				inSingle = true
			case needle:
				return i
			}
		}
	}
	return -1
}
