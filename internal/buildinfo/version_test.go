package buildinfo

import "testing"

func TestEffectiveVersionUsesTaggedModuleBuild(t *testing.T) {
	tests := []struct {
		linked string
		main   string
		want   string
	}{
		{"0.1.0-dev", "v1.2.3", "v1.2.3"},
		{"0.1.0-dev", "v1.2.4-0.20260820120000-abcdef123456", "v1.2.4-0.20260820120000-abcdef123456"},
		{"0.1.0-dev", "(devel)", "0.1.0-dev"},
		{"0.1.0-dev", "", "0.1.0-dev"},
		{"v1.2.3", "v1.2.4", "v1.2.3"},
	}
	for _, test := range tests {
		if got := effectiveVersion(test.linked, test.main); got != test.want {
			t.Errorf("effectiveVersion(%q, %q) = %q, want %q", test.linked, test.main, got, test.want)
		}
	}
}
