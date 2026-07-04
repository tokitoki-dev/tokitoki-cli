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

func TestSyncRequiresAPIKey(t *testing.T) {
	client := newTestClient(t)
	claudeDir := t.TempDir()

	err := client.Sync(context.Background(), SyncOptions{ClaudeDir: claudeDir})
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
