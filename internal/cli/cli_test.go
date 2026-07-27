package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagescan"
)

// Scanning is offline work, so a missing API key only stops the upload half:
// Sync still ingests, then fails with the key requirement.
func TestSyncRequiresAPIKey(t *testing.T) {
	app := newApp(t)
	err := app.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("Sync() error = %v, want API key requirement", err)
	}
}

func TestIngestWorksWithoutAPIKey(t *testing.T) {
	app := newApp(t)
	if err := app.Ingest(); err != nil {
		t.Fatalf("Ingest() without API key = %v, want offline scan to succeed", err)
	}
}

func TestSetAPIKeyPersistsSettings(t *testing.T) {
	app := newApp(t)
	if err := app.SetAPIKey("tokitoki_test_key"); err != nil {
		t.Fatal(err)
	}

	settings, err := app.Agent.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.APIKey != "tokitoki_test_key" {
		t.Fatalf("APIKey = %q, want saved key", settings.APIKey)
	}
}

func TestGetAPIKeyWritesSavedKey(t *testing.T) {
	app := newApp(t)
	if err := app.SetAPIKey("tokitoki_test_key"); err != nil {
		t.Fatal(err)
	}
	out := app.Out.(*bytes.Buffer)
	out.Reset()

	if err := app.GetAPIKey(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "tokitoki_test_key\n" {
		t.Fatalf("GetAPIKey() output = %q, want saved key", got)
	}
}

func TestGetAPIKeyRequiresConfiguredKey(t *testing.T) {
	app := newApp(t)
	err := app.GetAPIKey()
	if err == nil || !strings.Contains(err.Error(), "API key is not configured") {
		t.Fatalf("GetAPIKey() error = %v, want missing key error", err)
	}
}

func newApp(t *testing.T) *App {
	t.Helper()
	dataDir := t.TempDir()
	fileStore, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	usageDB, err := usagedb.Open(store.UsageDBPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = usageDB.Close() })
	return &App{
		Agent:   agent.New(fileStore, slog.New(slog.NewTextHandler(io.Discard, nil))),
		UsageDB: usageDB,
		Scanner: usagescan.New(usageDB),
		Out:     &bytes.Buffer{},
	}
}
