package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestRunRejectsDaemonCommand(t *testing.T) {
	if code := run([]string{"daemon"}); code != 2 {
		t.Fatalf("run(daemon) = %d, want 2", code)
	}
}

func TestParseWorkerFlagsDefaultsToUserAgentDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags, ok := parseWorkerFlags("tokitoki __service-run", []string{"--interval", "30s"})
	if !ok {
		t.Fatal("parseWorkerFlags() ok = false, want true")
	}
	if flags.interval != 30*time.Second {
		t.Fatalf("interval = %s, want 30s", flags.interval)
	}
	if flags.claudeDir != filepath.Join(home, ".claude") {
		t.Fatalf("claude dir = %q, want default home dir", flags.claudeDir)
	}
	if flags.codexDir != filepath.Join(home, ".codex") {
		t.Fatalf("codex dir = %q, want default home dir", flags.codexDir)
	}
}

func TestParseServiceFlagsSupportsSystemInstall(t *testing.T) {
	flags, userService, ok := parseServiceFlags([]string{
		"--system",
		"--claude-dir", "/tmp/claude",
		"--codex-dir", "/tmp/codex",
		"--interval", "1m",
	})
	if !ok {
		t.Fatal("parseServiceFlags() ok = false, want true")
	}
	if userService {
		t.Fatal("userService = true, want false with --system")
	}
	if flags.claudeDir != "/tmp/claude" || flags.codexDir != "/tmp/codex" {
		t.Fatalf("dirs = %q %q, want explicit dirs", flags.claudeDir, flags.codexDir)
	}
	if flags.interval != time.Minute {
		t.Fatalf("interval = %s, want 1m", flags.interval)
	}
}

func TestRunServiceRejectsUnknownAction(t *testing.T) {
	if code := run([]string{"service", "bogus"}); code != 2 {
		t.Fatalf("run(service bogus) = %d, want 2", code)
	}
}
