// Package config carries the build-time identity of this binary's local
// state. It has no dependencies beyond the standard library so any package
// can read it without an import cycle.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ServerURL is the Tokitoki server this binary talks to — usage uploads,
// update checks, key verification, the dashboard link, all of it. Like
// DataDirName it is a build parameter, and for the same reason: which server
// a binary reports to is a property of the build, not of whoever happens to
// run it. It is deliberately not read from the environment at run time. A
// development binary launched from an app, an editor plugin, a systemd unit
// or a bare shell must reach the same server in every case, and an override
// that lives in the environment is exactly what is missing in one of them.
//
//	-ldflags "-X github.com/tokitoki-dev/tokitoki-cli/internal/config.ServerURL=http://localhost:9093"
//
// The default is production, because a binary that carries no stamp at all is
// an installed one (`go install <module>@vX.Y.Z` bypasses the Makefile). The
// Makefile stamps localhost into every local target, so `make build` cannot
// reach production, whatever else goes wrong; only the release targets stamp
// the production address.
var ServerURL = "https://tokitoki.dev"

// DataDirName is the hidden directory, under the user's home directory, where
// this binary keeps all of its state. Every native front-end resolves the same
// path on macOS, Windows, and Linux: filepath.Join(os.UserHomeDir(), config.DataDirName).
//
// It is a build parameter. Set it to whatever a given build should own:
//
//	-ldflags "-X github.com/tokitoki-dev/tokitoki-cli/internal/config.DataDirName=.tokitoki-dev"
//
// Nothing in the code knows which values are meaningful, and nothing branches
// on the value it finds — binaries stamped with different names simply share
// no state: not the API key, not the event queue, not the locks. That is the
// whole mechanism, and it is why any number of builds coexist on one machine.
//
// The default is the directory installed binaries own, because a build that
// carries no stamp at all is an installed one: `go install <module>@vX.Y.Z`
// bypasses the project Makefile, and the binary it produces has to find the
// user's existing key and history rather than start from an empty directory.
//
// Development builds are the ones that get stamped. The Makefile stamps
// ~/.tokitoki-dev into every local target, so `make` and `make build` cannot
// touch installed state; only a build run outside the Makefile inherits this
// default.
var DataDirName = ".tokitoki"

// Validate reports whether DataDirName is a usable directory name. The value
// arrives from a build command line, where a typo — an absolute path, a stray
// quote, an empty value — would otherwise produce a binary that quietly keeps
// its state somewhere nobody looks. Callers resolve the data directory
// through it so that mistake surfaces as a startup error instead.
//
// It judges the shape of the name, never the name itself: which directory a
// build should own is the build's business, not this package's.
func Validate() error {
	if DataDirName == "" {
		return fmt.Errorf("data directory name is empty; check the -X config.DataDirName build stamp")
	}
	if strings.ContainsAny(DataDirName, `/\`) || DataDirName == "." || DataDirName == ".." {
		return fmt.Errorf("data directory name %q must be a single directory name, not a path; check the -X config.DataDirName build stamp", DataDirName)
	}
	parsed, err := url.Parse(strings.TrimSpace(ServerURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("server URL %q must be an http(s) URL with a host; check the -X config.ServerURL build stamp", ServerURL)
	}
	return nil
}
