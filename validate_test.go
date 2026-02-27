package main

import "testing"

func TestValidateProfileName(t *testing.T) {
	t.Parallel()

	valid := []string{"personal", "work-1", "team.alpha", "ops_prod", "me@example.com"}
	for _, name := range valid {
		name := name
		t.Run("valid_"+name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateProfileName(name); err != nil {
				t.Fatalf("expected valid name %q, got error: %v", name, err)
			}
		})
	}

	invalid := []string{"", "bad name", "bad/name", "bad$name"}
	for _, name := range invalid {
		name := name
		t.Run("invalid_"+name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateProfileName(name); err == nil {
				t.Fatalf("expected invalid name %q", name)
			}
		})
	}
}
