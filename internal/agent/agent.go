package agent

import (
	"log/slog"
)

type Settings struct {
	APIKey string `json:"api_key"`
	// InstallationID is this install's stable random identity. The server
	// keys device rows on it, so it must be unique per machine and constant
	// across runs — the store generates it once and persists it.
	InstallationID string `json:"installation_id"`
}

type Store interface {
	LoadSettings() (Settings, error)
	SaveAPIKey(apiKey string) error
}

type Agent struct {
	store  Store
	logger *slog.Logger
}

func New(store Store, logger *slog.Logger) *Agent {
	return &Agent{store: store, logger: logger}
}

func (a *Agent) Settings() (Settings, error) {
	return a.store.LoadSettings()
}

func (a *Agent) SaveAPIKey(apiKey string) error {
	return a.store.SaveAPIKey(apiKey)
}
