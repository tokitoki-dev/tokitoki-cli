// Package cli implements Tokitoki's one operation: scan local AI usage files
// and upload them to the local server.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagescan"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
)

// ErrNoAPIKey reports that no API key is configured. Callers distinguish it
// from every other failure — a missing key is a thing the user fixes, a
// network or server error is not — so it travels as a sentinel rather than as
// text for someone to pattern-match on.
var ErrNoAPIKey = errors.New("API key is not configured in ~/.tokitoki/api_key")

type App struct {
	Agent        *agent.Agent
	UsageDB      *usagedb.DB
	Scanner      *usagescan.Scanner
	ProviderDirs map[usage.Provider][]string
	Out          io.Writer
}

func (a *App) SetAPIKey(apiKey string) error {
	if err := a.Agent.SaveAPIKey(apiKey); err != nil {
		return err
	}
	return a.writeJSON(map[string]bool{"ok": true})
}

func (a *App) GetAPIKey() error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		return ErrNoAPIKey
	}
	_, err = fmt.Fprintf(a.Out, "%s\n", settings.APIKey)
	return err
}

// Sync scans the selected providers then uploads their events. The CLI avoids
// emitting counts or summaries: success only means the local files were
// processed and the server accepted the request.
//
// Without a configured API key the scan still runs and its events stay queued;
// only the upload half is skipped, so a user who has not signed in yet keeps
// accumulating history instead of losing it.
//
// Callers that coordinate multiple processes call the two phases separately —
// Ingest under the data lock, Upload under the upload lock — so a slow drain
// never blocks another process's ingestion.
func (a *App) Sync(ctx context.Context) error {
	if err := a.Ingest(); err != nil {
		return err
	}
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		return nil
	}
	return a.Upload(ctx)
}

// Ingest scans the selected providers into the shared local queue. Scanning
// is pure local work and needs no API key: events queue in the database until
// a key exists to upload them. It writes the database, so the caller holds
// the data lock.
func (a *App) Ingest() error {
	_, err := a.Scanner.Scan(a.ProviderDirs)
	return err
}

// Upload drains queued events to the server — the half of a sync that needs
// the API key.
func (a *App) Upload(ctx context.Context) error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		return ErrNoAPIKey
	}
	if err := usageupload.SyncPending(ctx, settings, a.UsageDB); err != nil {
		return err
	}
	return a.writeJSON(map[string]bool{"ok": true})
}

func (a *App) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.Out, "%s\n", data); err != nil {
		return err
	}
	return nil
}
