package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A build carrying no stamp is a development one. Stamps get forgotten — by
// `go test`, `go run`, an IDE, a front-end's build script — and what a
// forgetful build touches has to be the development directory and the
// development server, never a user's installed state or production. The
// installed identity is claimed explicitly by the release targets and checked
// by the release workflow.
func TestUnstampedBuildIsADevelopmentBuild(t *testing.T) {
	if DataDirName != ".tokitoki-dev" {
		t.Fatalf("DataDirName = %q by default, want .tokitoki-dev: an unstamped build must not own installed state", DataDirName)
	}
	if ServerURL != "http://localhost:9093" {
		t.Fatalf("ServerURL = %q by default, want the development server: an unstamped build must not reach production", ServerURL)
	}
	if err := Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the default to be usable", err)
	}
}

// The name is a build parameter, so any well-formed directory name has to be
// accepted — the package must not have opinions about which ones are real.
func TestValidateAcceptsAnyWellFormedName(t *testing.T) {
	original := DataDirName
	t.Cleanup(func() { DataDirName = original })

	for _, name := range []string{
		".tokitoki",
		".tokitoki-dev",
		".tokitoki-staging",
		".tokitoki-pr-1234",
		"tokitoki",
	} {
		DataDirName = name
		if err := Validate(); err != nil {
			t.Errorf("Validate() error = %v for %q, want nil", err, name)
		}
	}
}

// A stamp is typed by hand on a build command line. Every way of getting it
// wrong must be rejected at startup rather than silently keeping state in
// some path nobody will look in.
func TestValidateRejectsUnusableStamps(t *testing.T) {
	original := DataDirName
	t.Cleanup(func() { DataDirName = original })

	for _, name := range []string{
		"",
		".",
		"..",
		"/absolute/path",
		".tokitoki/nested",
		`.tokitoki\windows`,
	} {
		DataDirName = name
		if err := Validate(); err == nil {
			t.Errorf("Validate() error = nil for %q, want a rejection", name)
		}
	}
}

// The error must name the build flag: whoever sees it is looking at a
// mis-stamped binary, not a broken machine.
func TestValidateErrorPointsAtTheBuildStamp(t *testing.T) {
	original := DataDirName
	t.Cleanup(func() { DataDirName = original })

	DataDirName = filepath.Join("a", "b")
	err := Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want a rejection")
	}
	if !strings.Contains(err.Error(), "DataDirName") {
		t.Fatalf("error = %q, want it to name the build stamp", err)
	}
}
