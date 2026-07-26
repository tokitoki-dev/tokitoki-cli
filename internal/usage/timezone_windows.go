//go:build windows

package usage

import (
	"golang.org/x/sys/windows/registry"
)

// systemZoneName reads the machine's zone from the Windows registry and
// translates it into an IANA name.
//
// Windows keeps its own zone list rather than IANA's, so this is a two-step
// lookup: the registry holds a Windows key name ("Tokyo Standard Time"), and
// windows_zones.go maps that onto "Asia/Tokyo" using the Unicode CLDR table
// that Go itself ships. Without the translation the value would be useless to
// the server, which speaks IANA everywhere.
//
// TimeZoneKeyName is the value to read; StandardName is deliberately not used
// as a fallback because it is *localised* — on a Japanese-language Windows it
// holds "東京 (標準時)", which appears in no mapping table. TimeZoneKeyName is
// invariant across display languages, which is exactly what makes it usable.
//
// Returns "" if the key cannot be read (a locked-down profile, a stripped
// Windows image) or holds a name newer than this build's CLDR table. Reporting
// nothing is correct there — the caller treats a missing zone as unknown rather
// than guessing.
func systemZoneName() string {
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\TimeZoneInformation`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return ""
	}
	defer key.Close()

	windowsName, _, err := key.GetStringValue("TimeZoneKeyName")
	if err != nil {
		return ""
	}
	// Windows has been observed to pad this value with trailing NULs.
	windowsName = trimNulls(windowsName)

	iana, ok := windowsToIANA[windowsName]
	if !ok || !isLoadableZone(iana) {
		return ""
	}
	return iana
}

func trimNulls(value string) string {
	for len(value) > 0 && value[len(value)-1] == 0 {
		value = value[:len(value)-1]
	}
	return value
}
