package agentlib

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labx/tokitoki-agent/internal/config"
)

func TestNewUsesDefaultDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, config.DataDirName)
	if client.DataDir() != want {
		t.Fatalf("DataDir() = %q, want %q", client.DataDir(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestSetAndGetAPIKey(t *testing.T) {
	client := newTestClient(t)

	if err := client.SetAPIKey("  tokitoki_test_key \n"); err != nil {
		t.Fatal(err)
	}

	apiKey, err := client.GetAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "tokitoki_test_key" {
		t.Fatalf("GetAPIKey() = %q, want saved key", apiKey)
	}
}

func TestGetAPIKeyReturnsMissingError(t *testing.T) {
	client := newTestClient(t)

	_, err := client.GetAPIKey()
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("GetAPIKey() error = %v, want ErrMissingAPIKey", err)
	}
}

func TestSyncRejectsEmptyDirectories(t *testing.T) {
	client := newTestClient(t)

	err := client.Sync(context.Background(), SyncOptions{})
	if !errors.Is(err, ErrNoScanDirectories) {
		t.Fatalf("Sync() error = %v, want ErrNoScanDirectories", err)
	}
}

func TestNormalizeProviderDirsDropsEmptyDirectories(t *testing.T) {
	dirs := normalizeProviderDirs(map[Provider][]string{
		Provider("fixture"): {"fixture-dir"},
		ProviderCodex:       {""},
	})

	if got := dirs["fixture"]; len(got) != 1 || got[0] != "fixture-dir" {
		t.Fatalf("fixture dirs = %#v, want fixture-dir", got)
	}
	if got := dirs["codex"]; len(got) != 0 {
		t.Fatalf("codex dirs = %#v, want empty dirs dropped", got)
	}
}

func TestDefaultProviderDirsIncludesBuiltInProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs := DefaultProviderDirs()
	want := map[Provider]string{
		ProviderClaude:  filepath.Join(home, ".claude"),
		ProviderCodex:   filepath.Join(home, ".codex"),
		ProviderCopilot: filepath.Join(home, ".copilot", "otel"),
		ProviderGemini:  filepath.Join(home, ".gemini", "tmp"),
		ProviderKimi:    filepath.Join(home, ".kimi"),
		ProviderQwen:    filepath.Join(home, ".qwen"),
		ProviderPi:      filepath.Join(home, ".pi", "agent", "sessions"),
		ProviderAmp:     filepath.Join(home, ".local", "share", "amp"),
	}

	for provider, dir := range want {
		if got := dirs[provider]; len(got) == 0 || got[0] != dir {
			t.Fatalf("%s dirs = %#v, want first dir %q", provider, got, dir)
		}
	}
	if got := dirs[ProviderOpenClaw]; len(got) != 4 {
		t.Fatalf("openclaw dirs = %#v, want four defaults", got)
	}
}

func TestSyncRequiresAPIKey(t *testing.T) {
	client := newTestClient(t)
	claudeDir := t.TempDir()

	err := client.Sync(context.Background(), SyncOptions{
		ProviderDirs: map[Provider][]string{ProviderClaude: {claudeDir}},
	})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("Sync() error = %v, want API key requirement", err)
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
