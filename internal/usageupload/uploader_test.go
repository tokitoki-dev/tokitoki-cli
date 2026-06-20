package usageupload

import "testing"

func TestDefaultServerURLIsLocalhost(t *testing.T) {
	if DefaultServerURL != "http://localhost:9093" {
		t.Fatalf("DefaultServerURL = %q, want http://localhost:9093", DefaultServerURL)
	}
}
