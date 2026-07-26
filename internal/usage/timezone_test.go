package usage

import (
	"testing"
	"time"
)

func TestMachineTimezoneUsesTZWhenSet(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	if got := MachineTimezone(); got != "Asia/Tokyo" {
		t.Fatalf("MachineTimezone() = %q, want Asia/Tokyo", got)
	}
}

func TestMachineTimezoneAcceptsLeadingColon(t *testing.T) {
	// ":Asia/Tokyo" is legal TZ syntax; the colon names a file path by
	// convention and is not part of the zone name.
	t.Setenv("TZ", ":Asia/Tokyo")
	if got := MachineTimezone(); got != "Asia/Tokyo" {
		t.Fatalf("MachineTimezone() = %q, want Asia/Tokyo", got)
	}
}

func TestMachineTimezoneRejectsJunkTZ(t *testing.T) {
	// A bogus TZ must not be reported as fact. Falling through to the system
	// source is fine; echoing "Not/AZone" is not — the server would discard it
	// and Postgres raises on it.
	t.Setenv("TZ", "Not/AZone")
	if got := MachineTimezone(); got == "Not/AZone" {
		t.Fatal("MachineTimezone() reported an unloadable zone name")
	}
}

func TestMachineTimezoneRejectsPathTraversal(t *testing.T) {
	t.Setenv("TZ", "../../etc/passwd")
	if got := MachineTimezone(); got == "../../etc/passwd" {
		t.Fatal("MachineTimezone() echoed a traversal path")
	}
}

func TestMachineTimezoneReturnsLoadableNameOrEmpty(t *testing.T) {
	// The contract callers rely on: whatever comes back either loads as a real
	// zone or is empty. Never an abbreviation, never a guess.
	got := MachineTimezone()
	if got == "" {
		t.Skip("no zone resolvable on this machine")
	}
	if _, err := time.LoadLocation(got); err != nil {
		t.Fatalf("MachineTimezone() = %q, which does not load: %v", got, err)
	}
	if got == "Local" {
		t.Fatal("MachineTimezone() returned Go's placeholder rather than a name")
	}
}

func TestIsLoadableZone(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Tokyo", "America/New_York"} {
		if !isLoadableZone(name) {
			t.Errorf("isLoadableZone(%q) = false, want true", name)
		}
	}
	// "JST" is the case that matters: Go will hand out this abbreviation, and
	// it must never be reported, because IST/CST/BST style abbreviations map to
	// several different real zones.
	for _, name := range []string{"", "Not/AZone", "../etc/passwd", "JST"} {
		if isLoadableZone(name) {
			t.Errorf("isLoadableZone(%q) = true, want false", name)
		}
	}
}
