package codexstate

import (
	"fmt"
	"regexp"
	"strings"
)

var profileNamePattern = regexp.MustCompile(`^[a-z0-9@._-]+$`)

// ValidateProfileName enforces the profile-name contract shared by core
// commands and monitor discovery.
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("profile name too long (max 64 characters)")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("profile name cannot be %q", name)
	}
	if name != strings.ToLower(name) {
		return fmt.Errorf("profile name %q must be lowercase", name)
	}
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("invalid profile name %q. allowed characters: lowercase letters, numbers, at sign, dot, underscore, hyphen", name)
	}
	return nil
}
