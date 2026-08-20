package buildinfo

import "testing"

func TestEffectiveVersionDistinguishesSourceAndModuleBuilds(t *testing.T) {
	tests := []struct {
		linked      string
		main        string
		sourceBuild bool
		want        string
	}{
		{"0.1.0-dev", "v1.2.3", false, "v1.2.3"},
		{"0.1.0-dev", "v1.2.4-0.20260820120000-abcdef123456", false, "v1.2.4-0.20260820120000-abcdef123456"},
		{"0.1.0-dev", "v1.2.4-0.20260820120000-abcdef123456", true, "0.1.0-dev"},
		{"0.1.0-dev", "(devel)", false, "0.1.0-dev"},
		{"0.1.0-dev", "", false, "0.1.0-dev"},
		{"v1.2.3", "v1.2.4", true, "v1.2.3"},
	}
	for _, test := range tests {
		if got := effectiveVersion(test.linked, test.main, test.sourceBuild); got != test.want {
			t.Errorf("effectiveVersion(%q, %q, %t) = %q, want %q", test.linked, test.main, test.sourceBuild, got, test.want)
		}
	}
}
