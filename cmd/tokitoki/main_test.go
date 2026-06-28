package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/labx/tokitoki-agent/internal/config"
)

func TestRunRejectsMissingDirs(t *testing.T) {
	if code := run([]string{}); code != 2 {
		t.Fatalf("run() with no dirs = %d, want 2", code)
	}
}

func TestRunSetKeyWritesAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	path := filepath.Join(home, config.DataDirName, "api_key")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tokitoki_test_key\n" {
		t.Fatalf("api_key = %q, want saved key", string(data))
	}
}

func TestRunSetKeyRejectsMissingKey(t *testing.T) {
	if code := run([]string{"set", "key"}); code != 2 {
		t.Fatalf("run(set key) = %d, want 2", code)
	}
}

func TestRunGetKeyReturnsErrorWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if code := run([]string{"get", "key"}); code != 1 {
		t.Fatalf("run(get key) = %d, want 1", code)
	}
}

func TestRunGetKeyRejectsExtraArgs(t *testing.T) {
	if code := run([]string{"get", "key", "extra"}); code != 2 {
		t.Fatalf("run(get key extra) = %d, want 2", code)
	}
}
