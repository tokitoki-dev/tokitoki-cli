package agent

import (
	"log/slog"
)

type Settings struct {
	APIKey string `json:"api_key"`
	// Hostname is the user's override for the label uploads carry for this
	// machine. Empty means "the system hostname" — see usageupload.DeviceName.
	Hostname string `json:"hostname,omitempty"`
}

type Store interface {
	LoadSettings() (Settings, error)
	SaveAPIKey(apiKey string) error
	SaveHostname(hostname string) error
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

func (a *Agent) SaveHostname(hostname string) error {
	return a.store.SaveHostname(hostname)
}
