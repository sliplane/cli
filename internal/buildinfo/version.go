package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version and Commit are set with -ldflags by make and release builds.
var (
	Version = "dev"
	Commit  = "unknown"
)

// init falls back to Go's module metadata for go install and plain go build.
func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
	for _, setting := range info.Settings {
		if Commit == "unknown" && setting.Key == "vcs.revision" {
			Commit = setting.Value
		}
	}
}
