// Package config carries the build-time identity of this binary's local
// state. It has no dependencies beyond the standard library so any package
// can read it without an import cycle.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// A build has an identity: the server it reports to and the directory, under
// the user's home, where it keeps its state. Both are build parameters, never
// read from the environment at run time — a binary launched from an app, an
// editor plugin, a systemd unit or a bare shell must be the same binary in
// every case, and an override that lives in the environment is exactly what
// is missing in one of them. `tokitoki server-url` and `tokitoki data-dir`
// print what a binary was built with.
//
// The defaults are the DEVELOPMENT identity, and that direction is the whole
// design. Stamps get forgotten: `go test`, `go run`, an IDE's debug button and
// a front-end's build script that remembered the version and nothing else all
// produce an unstamped binary. When unstamped meant installed, every one of
// those read the user's real key, wrote to their real queue, and talked to
// production — tests included, which pinged the live server on every run.
// Forgetting has to be safe, so forgetting gets you the development server
// and a directory no installed binary touches.
//
// The installed identity is therefore something a build must claim out loud:
//
//	-ldflags "-X .../internal/config.ServerURL=https://tokitoki.dev
//	          -X .../internal/config.DataDirName=.tokitoki"
//
// Only the Makefile's release targets do, and the release workflow runs the
// built binary and refuses to publish one that reports anything else — so the
// failure mode of a lost stamp is a failed release, not a quiet one. `go
// install <module>@vX.Y.Z` bypasses the Makefile and so yields a development
// binary; it is not a supported way to install Tokitoki. Releases are.

// ServerURL is the Tokitoki server this binary talks to — usage uploads,
// update checks, key verification, the dashboard link, all of it.
var ServerURL = "http://localhost:9093"

// DataDirName is the hidden directory, under the user's home directory, where
// this binary keeps all of its state: filepath.Join(os.UserHomeDir(),
// config.DataDirName) on macOS, Windows, and Linux alike.
//
// Nothing in the code knows which values are meaningful, and nothing branches
// on the value it finds — binaries stamped with different names simply share
// no state: not the API key, not the event queue, not the locks. That is the
// whole mechanism, and it is why any number of builds coexist on one machine.
var DataDirName = ".tokitoki-dev"

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
