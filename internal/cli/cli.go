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
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
	"github.com/labx/tokitoki-agent/internal/usageupload"
)

type App struct {
	Agent        *agent.Agent
	UsageDB      *usagedb.DB
	Scanner      *usagescan.Scanner
	ProviderDirs map[usage.Provider][]string
	ClaudeDir    string
	CodexDir     string
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
	if _, err := a.Scanner.ScanProviders(a.scanProviderDirs()); err != nil {
		return err
	}
	events, err := a.UsageDB.PendingUsageEvents(0)
	if err != nil {
		return err
	}
	_, err = usageupload.UploadEach(ctx, settings, events, func(_ []usage.Entry, response usageupload.Response) error {
		uploaded := append([]string{}, response.Accepted...)
		uploaded = append(uploaded, response.Duplicate...)
		if err := a.UsageDB.MarkEventsUploaded(uploaded); err != nil {
			return err
		}
		if err := a.UsageDB.MarkEventsRejected(rejectedReasons(response.Rejected)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		var batchError usageupload.BatchError
		if errors.As(err, &batchError) {
			_ = a.UsageDB.MarkEventsUploadFailed(eventIDs(batchError.Events), err.Error())
		}
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

func (a *App) scanProviderDirs() map[usage.Provider][]string {
	providerDirs := make(map[usage.Provider][]string, len(a.ProviderDirs)+2)
	for provider, dirs := range a.ProviderDirs {
		providerDirs[provider] = append([]string{}, dirs...)
	}
	if a.ClaudeDir != "" {
		providerDirs[usage.ProviderClaude] = append(providerDirs[usage.ProviderClaude], a.ClaudeDir)
	}
	if a.CodexDir != "" {
		providerDirs[usage.ProviderCodex] = append(providerDirs[usage.ProviderCodex], a.CodexDir)
	}
	return providerDirs
}

func eventIDs(events []usage.Entry) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		if event.ID != "" {
			ids = append(ids, event.ID)
		}
	}
	return ids
}

func rejectedReasons(rejected []usageupload.Reject) map[string]string {
	reasons := make(map[string]string, len(rejected))
	for _, item := range rejected {
		if item.ID != "" {
			reasons[item.ID] = item.Reason
		}
	}
	return reasons
}
