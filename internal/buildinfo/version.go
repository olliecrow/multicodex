package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is replaced with a release tag through -ldflags.
var Version = "0.1.0-dev"

// Current also recognizes module-proxy builds produced by go install @version.
func Current() string {
	mainVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		mainVersion = info.Main.Version
	}
	return effectiveVersion(Version, mainVersion)
}

func effectiveVersion(linked, main string) string {
	linked = strings.TrimSpace(linked)
	main = strings.TrimSpace(main)
	if strings.HasSuffix(linked, "-dev") && main != "" && main != "(devel)" {
		return main
	}
	return linked
}
