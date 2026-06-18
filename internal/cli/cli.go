// Package cli implements the tokitoki agent's subcommands.
//
// The agent is a stateless command-line tool, not a daemon: each invocation
// opens the local data directory, does one unit of work, writes a JSON result
// to stdout, and exits. There is no long-lived process and no local HTTP
// server — native front-ends drive the agent by exec'ing it and parsing
// stdout, and background cadence is owned by an OS scheduler running
// `tokitoki sync`. Durable state (usage.bolt, config) lives on disk and is
// shared across invocations.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/claudeusage"
	"github.com/labx/tokitoki-agent/internal/codexusage"
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
	"github.com/labx/tokitoki-agent/internal/usageupload"
)

// App holds the dependencies the subcommands operate on. UsageDB and Scanner
// are only set for commands that index or read the local usage database; the
// others (config) leave them nil so no BoltDB lock is taken.
type App struct {
	Agent   *agent.Agent
	UsageDB *usagedb.DB
	Scanner *usagescan.Scanner
	Out     io.Writer
}

// Scan indexes changed Claude and Codex session files into the local database.
func (a *App) Scan() error {
	result, err := a.Scanner.ScanAll()
	if err != nil {
		return err
	}
	return a.writeJSON(result)
}

// Upload sends all indexed usage events to the configured server.
func (a *App) Upload(ctx context.Context) error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	events, err := a.UsageDB.UsageEvents()
	if err != nil {
		return err
	}
	response, err := usageupload.Upload(ctx, settings, events)
	if err != nil {
		return err
	}
	return a.writeJSON(map[string]any{
		"ok":         true,
		"batch_id":   response.BatchID,
		"events":     len(events),
		"accepted":   len(response.Accepted),
		"duplicate":  len(response.Duplicate),
		"server_url": firstNonEmpty(settings.ServerURL, usageupload.DefaultServerURL),
	})
}

// Sync scans changed session files and then uploads. This is the single
// command an OS scheduler (launchd / systemd timer / Task Scheduler) runs on an
// interval to keep the server up to date even when no front-end is open.
func (a *App) Sync(ctx context.Context) error {
	scanResult, err := a.Scanner.ScanAll()
	if err != nil {
		return err
	}
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	events, err := a.UsageDB.UsageEvents()
	if err != nil {
		return err
	}
	response, err := usageupload.Upload(ctx, settings, events)
	if err != nil {
		return err
	}
	return a.writeJSON(map[string]any{
		"ok":         true,
		"scan":       scanResult,
		"events":     len(events),
		"accepted":   len(response.Accepted),
		"duplicate":  len(response.Duplicate),
		"server_url": firstNonEmpty(settings.ServerURL, usageupload.DefaultServerURL),
	})
}

type sourceResult struct {
	Provider usage.Provider `json:"provider"`
	Paths    []string       `json:"paths,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// providerSources resolves the on-disk session directories for the requested
// provider ("all", "claude", or "codex"), recording any lookup error per
// source rather than failing the whole command.
func providerSources(provider string) ([]sourceResult, error) {
	sources := make([]sourceResult, 0, 2)
	if provider == "all" || provider == string(usage.ProviderClaude) {
		paths, err := claudeusage.ClaudePaths()
		source := sourceResult{Provider: usage.ProviderClaude, Paths: paths}
		if err != nil {
			source.Error = err.Error()
		}
		sources = append(sources, source)
	}
	if provider == "all" || provider == string(usage.ProviderCodex) {
		paths, err := codexusage.CodexPaths()
		source := sourceResult{Provider: usage.ProviderCodex, Paths: paths}
		if err != nil {
			source.Error = err.Error()
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil, errors.New("provider must be claude, codex, or all")
	}
	return sources, nil
}

// Daily summarizes indexed AI token usage by day and project. provider is
// "all", "claude", or "codex"; project optionally filters by name or path.
func (a *App) Daily(provider, project string) error {
	if provider == "" {
		provider = "all"
	}
	sources, err := providerSources(provider)
	if err != nil {
		return err
	}

	summaries, err := a.UsageDB.DailyProjectSummaries(provider, project)
	if err != nil {
		return err
	}
	return a.writeJSON(map[string]any{
		"sources": sources,
		"data":    summaries,
	})
}

// ClaudeDaily summarizes Claude usage by reading session files directly,
// without consulting the indexed database.
func (a *App) ClaudeDaily(project string) error {
	paths, err := claudeusage.ClaudePaths()
	if err != nil {
		return err
	}
	summaries, err := claudeusage.DailyProjectSummaries(project)
	if err != nil {
		return err
	}
	return a.writeJSON(map[string]any{
		"paths": paths,
		"data":  summaries,
	})
}

// ConfigGet prints the current agent settings.
func (a *App) ConfigGet() error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	return a.writeJSON(settings)
}

// ConfigSet updates the agent settings. Nil fields are left unchanged.
func (a *App) ConfigSet(apiKey, serverURL *string) error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	if apiKey != nil {
		settings.APIKey = *apiKey
	}
	if serverURL != nil {
		settings.ServerURL = *serverURL
	}
	if err := a.Agent.SaveSettings(settings); err != nil {
		return err
	}
	return a.writeJSON(map[string]any{"ok": true})
}

// Status reports what the agent can know without a server round-trip: how many
// usage events are indexed locally, where the session files live, and the
// configured upload target.
func (a *App) Status() error {
	settings, err := a.Agent.Settings()
	if err != nil {
		return err
	}
	indexedEvents, err := a.UsageDB.CountEvents()
	if err != nil {
		return err
	}
	sources, err := providerSources("all")
	if err != nil {
		return err
	}
	return a.writeJSON(map[string]any{
		"indexed_events": indexedEvents,
		"server_url":     firstNonEmpty(settings.ServerURL, usageupload.DefaultServerURL),
		"has_api_key":    settings.APIKey != "",
		"sources":        sources,
	})
}

func (a *App) writeJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.Out, "%s\n", data); err != nil {
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
