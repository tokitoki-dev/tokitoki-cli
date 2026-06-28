// Package cli implements TokiToki's one operation: scan local AI usage files
// and upload them to the local server.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
	"github.com/labx/tokitoki-agent/internal/usageupload"
)

type App struct {
	Agent     *agent.Agent
	UsageDB   *usagedb.DB
	Scanner   *usagescan.Scanner
	ClaudeDir string
	CodexDir  string
	Out       io.Writer
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
		return errors.New("API key is not configured in ~/.tokitoki/api_key")
	}
	_, err = fmt.Fprintf(a.Out, "%s\n", settings.APIKey)
	return err
}

// Sync scans the selected providers then uploads their events. The CLI avoids
// emitting counts or summaries: success only means the local files were
// processed and the server accepted the request.
func (a *App) Sync(ctx context.Context) error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		return errors.New("API key is required in ~/.tokitoki/api_key")
	}
	if _, err := a.Scanner.Scan(a.ClaudeDir, a.CodexDir); err != nil {
		return err
	}
	events, err := a.UsageDB.UsageEvents()
	if err != nil {
		return err
	}
	if _, err := usageupload.Upload(ctx, settings, events); err != nil {
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
