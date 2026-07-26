//go:build !windows

package usage

import (
	"os"
	"path/filepath"
	"strings"
)

// zoneinfoMarker is the one path segment every tzdata layout shares: the IANA
// name is whatever follows it.
//
// Matching on this rather than a fixed prefix is what makes the lookup work on
// macOS, where /etc/localtime resolves through a versioned directory —
// /private/var/db/timezone/tz/2026b.1.0/zoneinfo/Asia/Tokyo — that no
// hard-coded prefix would match. Linux resolves to /usr/share/zoneinfo/... and
// falls out of the same rule.
const zoneinfoMarker = "/zoneinfo/"

// systemZoneName reads the IANA name out of /etc/localtime, which is where Unix
// records the machine's zone.
//
// Returns "" when /etc/localtime is a copied file rather than a symlink (some
// minimal images do this, and a copy carries no name), or when the extracted
// name is not one tzdata knows.
func systemZoneName() string {
	resolved, err := filepath.EvalSymlinks("/etc/localtime")
	if err != nil {
		return ""
	}
	if name := zoneNameFromPath(resolved); name != "" {
		return name
	}
	// Some distributions record the name in a plain text file instead. Debian
	// has always had /etc/timezone; several others follow it.
	for _, path := range []string{"/etc/timezone"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(contents))
		if isLoadableZone(name) {
			return name
		}
	}
	return ""
}

func zoneNameFromPath(resolved string) string {
	resolved = filepath.ToSlash(resolved)
	index := strings.LastIndex(resolved, zoneinfoMarker)
	if index < 0 {
		return ""
	}
	name := resolved[index+len(zoneinfoMarker):]
	if !isLoadableZone(name) {
		return ""
	}
	return name
}
