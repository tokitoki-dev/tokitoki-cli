// Package buildinfo carries the values stamped into release binaries. It has
// no dependencies beyond the standard library, so any package — main, the
// uploader, future telemetry — can read the version without an import cycle.
package buildinfo

import (
	"regexp"
	"runtime"
	"runtime/debug"
)

// Name identifies this CLI in reported requests. It is the product half of the
// User-Agent, kept here so every caller reports the same name.
const Name = "tokitoki-cli"

// Version is stamped by the release build:
//
//	-ldflags "-X github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo.Version=1.2.0"
//
// The default "dev" marks a local build, which never self-updates.
var Version = "dev"

// releaseVersion matches an exact tagged release — no pseudo-version suffix,
// no +dirty marker. Only these may identify a binary as a release.
var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// Resolved is the version this binary should report. Release builds carry the
// stamped Version. Binaries built by `go install module@vX.Y.Z` skip the
// Makefile, so no stamp — for those, the module version Go records in the
// binary is the release tag, and we trust it. Anything vaguer (a
// pseudo-version of some commit, a +dirty local build) stays "dev": a binary
// we cannot pin to a published release must never call itself one, or it
// would self-update over work in progress.
func Resolved() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok &&
		releaseVersion.MatchString(info.Main.Version) {
		return info.Main.Version
	}
	return Version
}

// UserAgent is the value every reported request sends as its User-Agent header,
// e.g. "tokitoki-cli/1.2.0 (darwin; arm64)". Reporting it from one place keeps
// the name, version, OS and CPU architecture consistent across callers, so the
// server can measure version fragmentation from a single field.
func UserAgent() string {
	return Name + "/" + Resolved() + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}
