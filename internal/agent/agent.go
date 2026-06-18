package agent

import (
	"log/slog"
	"strings"
)

type Settings struct {
	APIKey    string `json:"api_key"`
	ServerURL string `json:"server_url"`
}

type Store interface {
	LoadSettings() (Settings, error)
	SaveSettings(Settings) error
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

func (a *Agent) SaveSettings(settings Settings) error {
	settings.ServerURL = strings.TrimRight(strings.TrimSpace(settings.ServerURL), "/")
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	return a.store.SaveSettings(settings)
}
