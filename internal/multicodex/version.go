package multicodex

import "github.com/olliecrow/multicodex/internal/buildinfo"

const appName = "multicodex"

func version() string {
	return buildinfo.Version
}
