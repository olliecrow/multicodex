package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is replaced with a release tag through -ldflags.
var Version = "0.1.0-dev"

// Current also recognizes module-proxy builds produced by go install @version.
func Current() string {
	mainVersion, sourceBuild := "", false
	if info, ok := debug.ReadBuildInfo(); ok {
		mainVersion = info.Main.Version
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				sourceBuild = true
				break
			}
		}
	}
	return effectiveVersion(Version, mainVersion, sourceBuild)
}

func effectiveVersion(linked, main string, sourceBuild bool) string {
	linked = strings.TrimSpace(linked)
	main = strings.TrimSpace(main)
	if strings.HasSuffix(linked, "-dev") && !sourceBuild && main != "" && main != "(devel)" {
		return main
	}
	return linked
}
