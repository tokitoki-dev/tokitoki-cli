package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labx/tokitoki-agent/internal/config"
	"github.com/labx/tokitoki-agent/pkg/agentlib"
)

func TestParseRunFlagsDefaultsToProviderDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags, ok := parseRunFlags(nil)
	if !ok {
		t.Fatal("parseRunFlags() ok = false, want true")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != filepath.Join(home, ".claude") {
		t.Fatalf("claude dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != filepath.Join(home, ".codex") {
		t.Fatalf("codex dirs = %#v, want default home dir", got)
	}
}

func TestParseRunFlagsRejectsEmptyProviderDir(t *testing.T) {
	if _, ok := parseRunFlags([]string{"--provider-dir", "claude="}); ok {
		t.Fatal("parseRunFlags() ok = true, want false")
	}
}

func TestParseRunFlagsUsesExplicitProviderDirs(t *testing.T) {
	flags, ok := parseRunFlags([]string{
		"--provider-dir", "claude=/tmp/claude",
		"--provider-dir", "codex=/tmp/codex",
	})
	if !ok {
		t.Fatal("parseRunFlags() ok = false, want true")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != "/tmp/claude" {
		t.Fatalf("claude dirs = %#v, want explicit dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != "/tmp/codex" {
		t.Fatalf("codex dirs = %#v, want explicit dir", got)
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
	path := filepath.Join(home, config.DataDirName, "api_key")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "" {
		t.Fatalf("api_key = %q, want empty file", string(data))
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

func TestParseWorkerFlagsDefaultsToProviderDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags, ok := parseWorkerFlags("tokitoki __service-run", []string{"--interval", "30s"})
	if !ok {
		t.Fatal("parseWorkerFlags() ok = false, want true")
	}
	if flags.interval != 30*time.Second {
		t.Fatalf("interval = %s, want 30s", flags.interval)
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != filepath.Join(home, ".claude") {
		t.Fatalf("claude dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != filepath.Join(home, ".codex") {
		t.Fatalf("codex dirs = %#v, want default home dir", got)
	}
}

func TestParseServiceFlagsSupportsSystemInstall(t *testing.T) {
	flags, userService, ok := parseServiceFlags([]string{
		"--system",
		"--provider-dir", "claude=/tmp/claude",
		"--provider-dir", "codex=/tmp/codex",
		"--interval", "1m",
	})
	if !ok {
		t.Fatal("parseServiceFlags() ok = false, want true")
	}
	if userService {
		t.Fatal("userService = true, want false with --system")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != "/tmp/claude" {
		t.Fatalf("claude dirs = %#v, want explicit dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != "/tmp/codex" {
		t.Fatalf("codex dirs = %#v, want explicit dir", got)
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
