package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/store"
)

func TestSyncRequiresAPIKey(t *testing.T) {
	app := newApp(t)
	err := app.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("Sync() error = %v, want API key requirement", err)
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
	fileStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &App{
		Agent: agent.New(fileStore, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Out:   &bytes.Buffer{},
	}
}
