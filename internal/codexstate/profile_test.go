package codexstate

import (
	"strings"
	"testing"
)

func TestValidateProfileName(t *testing.T) {
	t.Parallel()

	valid := []string{"personal", "work-1", "team.alpha", "ops_prod", "me@example.com"}
	for _, name := range valid {
		t.Run("valid_"+name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateProfileName(name); err != nil {
				t.Fatalf("expected valid name %q, got error: %v", name, err)
			}
		})
	}

	invalid := []string{"", ".", "..", "Work", "bad name", "bad/name", "bad$name", strings.Repeat("a", 65)}
	for _, name := range invalid {
		t.Run("invalid_"+name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateProfileName(name); err == nil {
				t.Fatalf("expected invalid name %q", name)
			}
		})
	}
}
