package agent

import (
	"log/slog"
)

type Settings struct {
	APIKey string `json:"api_key"`
}

type Store interface {
	LoadSettings() (Settings, error)
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
