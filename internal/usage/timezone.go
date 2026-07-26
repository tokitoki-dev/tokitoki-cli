package usage

import (
	"os"
	"strings"
	"time"
)

// MachineTimezone reports the IANA zone this machine is in, or "" when it
// genuinely cannot be determined.
//
// Why this is not one line of standard library: Go's time.Local reports the
// literal string "Local" on every platform, and the only name it will hand out
// is an abbreviation ("JST"), which is ambiguous — IST is India, Ireland and
// Israel; CST is China, US Central and Cuba. An abbreviation cannot be expanded
// back into a zone, so it is useless for analysis.
//
// The name is therefore looked for in three places, most authoritative first:
//
//  1. $TZ, because that is what Go itself obeys — when it is set it *is* this
//     process's zone, whatever the OS thinks.
//  2. time.Local's own name, on the off chance a platform populates it.
//  3. The OS's own record of the choice, which is where the answer actually
//     lives: the /etc/localtime symlink on Unix, the registry on Windows.
//     See timezone_unix.go and timezone_windows.go.
//
// Callers must handle "". A stripped container with no tzdata and no TZ set has
// no zone to report, and inventing one would be worse than admitting it.
func MachineTimezone() string {
	if name := os.Getenv("TZ"); name != "" {
		// A leading colon is legal in TZ (":Asia/Tokyo") and names a file path
		// by convention; strip it before validating.
		name = strings.TrimPrefix(name, ":")
		if isLoadableZone(name) {
			return name
		}
	}
	// "Local" and "UTC" are Go's placeholders rather than answers. UTC is
	// rejected deliberately: a machine reporting it is far more often one with
	// no zone configured than one genuinely in Greenwich, and a confidently
	// wrong name is worse than none.
	if name := time.Local.String(); name != "" && name != "Local" && name != "UTC" {
		if isLoadableZone(name) {
			return name
		}
	}
	return systemZoneName()
}

// isLoadableZone is the gate every candidate passes before being reported.
//
// It keeps a name this machine's tzdata cannot resolve from being sent as fact:
// the server would only discard it, and Postgres raises outright on an unknown
// name in AT TIME ZONE. The "path traversal" check matters because $TZ is
// attacker-controllable in some deployments and Go resolves it against the
// zoneinfo directory.
func isLoadableZone(name string) bool {
	if name == "" || strings.Contains(name, "..") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
