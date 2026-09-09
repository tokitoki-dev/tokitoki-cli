package machineid

import (
	"regexp"
	"testing"
)

// ID must be deterministic and either empty or a full SHA-256 hex digest —
// the server's validation rejects anything else.
func TestIDShapeAndDeterminism(t *testing.T) {
	first := ID()
	second := ID()
	if first != second {
		t.Fatalf("ID not deterministic: %q vs %q", first, second)
	}
	if first == "" {
		t.Skip("no machine id available on this system")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("ID %q is not a sha256 hex digest", first)
	}
}
