package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagescan"
)

// Scanning is offline work, so a missing API key only skips the upload half:
// Sync ingests and returns cleanly, leaving events queued for a later run.
func TestSyncWithoutAPIKeyScansOffline(t *testing.T) {
	app := newApp(t)
	if err := app.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() without API key = %v, want offline scan to succeed", err)
	}
}

func TestIngestWorksWithoutAPIKey(t *testing.T) {
	app := newApp(t)
	if err := app.Ingest(); err != nil {
		t.Fatalf("Ingest() without API key = %v, want offline scan to succeed", err)
	}
}

// Upload is the half that needs the key, so calling it directly still fails.
func TestUploadRequiresAPIKey(t *testing.T) {
	app := newApp(t)
	err := app.Upload(context.Background())
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("Upload() error = %v, want ErrNoAPIKey", err)
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
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("GetAPIKey() error = %v, want ErrNoAPIKey", err)
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
