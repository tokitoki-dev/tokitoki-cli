package main

import "testing"

func TestRunRejectsMissingDirs(t *testing.T) {
	if code := run([]string{}); code != 2 {
		t.Fatalf("run() with no dirs = %d, want 2", code)
	}
}
