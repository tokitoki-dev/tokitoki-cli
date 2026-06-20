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
