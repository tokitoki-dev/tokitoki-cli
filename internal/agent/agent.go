package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Heartbeat struct {
	Time     time.Time `json:"time"`
	Entity   string    `json:"entity"`
	Project  string    `json:"project"`
	Language string    `json:"language"`
	Editor   string    `json:"editor"`
	Type     string    `json:"type"`
}

type Settings struct {
	APIKey    string `json:"api_key"`
	ServerURL string `json:"server_url"`
}

type Status struct {
	Running         bool       `json:"running"`
	QueueSize       int        `json:"queue_size"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	LastSyncAt      *time.Time `json:"last_sync_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

type Store interface {
	Token() (string, error)
	LoadSettings() (Settings, error)
	SaveSettings(Settings) error
	AppendHeartbeat(Heartbeat) error
	Heartbeats() ([]Heartbeat, error)
	ReplaceHeartbeats([]Heartbeat) error
	QueueSize() (int, error)
}

type Agent struct {
	store  Store
	logger *slog.Logger

	mu              sync.RWMutex
	lastHeartbeatAt *time.Time
	lastSyncAt      *time.Time
	lastError       string
}

func New(store Store, logger *slog.Logger) *Agent {
	return &Agent{store: store, logger: logger}
}

func (a *Agent) Token() (string, error) {
	return a.store.Token()
}

func (a *Agent) Settings() (Settings, error) {
	return a.store.LoadSettings()
}

func (a *Agent) SaveSettings(settings Settings) error {
	settings.ServerURL = strings.TrimRight(strings.TrimSpace(settings.ServerURL), "/")
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	return a.store.SaveSettings(settings)
}

func (a *Agent) RecordHeartbeat(heartbeat Heartbeat) error {
	if err := validateHeartbeat(heartbeat); err != nil {
		return err
	}

	if heartbeat.Time.IsZero() {
		heartbeat.Time = time.Now().UTC()
	}

	if err := a.store.AppendHeartbeat(heartbeat); err != nil {
		a.setError(err)
		return err
	}

	a.mu.Lock()
	a.lastHeartbeatAt = &heartbeat.Time
	a.lastError = ""
	a.mu.Unlock()

	return nil
}

func (a *Agent) Status() (Status, error) {
	queueSize, err := a.store.QueueSize()
	if err != nil {
		return Status{}, err
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	return Status{
		Running:         true,
		QueueSize:       queueSize,
		LastHeartbeatAt: cloneTime(a.lastHeartbeatAt),
		LastSyncAt:      cloneTime(a.lastSyncAt),
		LastError:       a.lastError,
	}, nil
}

func (a *Agent) Sync(ctx context.Context) (Status, error) {
	settings, err := a.store.LoadSettings()
	if err != nil {
		a.setError(err)
		return Status{}, err
	}

	heartbeats, err := a.store.Heartbeats()
	if err != nil {
		a.setError(err)
		return Status{}, err
	}

	if len(heartbeats) == 0 {
		a.markSynced()
		return a.Status()
	}

	if settings.APIKey == "" || settings.ServerURL == "" {
		err := errors.New("sync skipped: api_key and server_url are required")
		a.setError(err)
		return a.Status()
	}

	if err := postHeartbeats(ctx, settings, heartbeats); err != nil {
		a.setError(err)
		return a.Status()
	}

	if err := a.store.ReplaceHeartbeats(nil); err != nil {
		a.setError(err)
		return Status{}, err
	}

	a.markSynced()
	return a.Status()
}

func validateHeartbeat(heartbeat Heartbeat) error {
	if strings.TrimSpace(heartbeat.Entity) == "" {
		return errors.New("entity is required")
	}
	if strings.TrimSpace(heartbeat.Type) == "" {
		return errors.New("type is required")
	}
	return nil
}

func postHeartbeats(ctx context.Context, settings Settings, heartbeats []Heartbeat) error {
	body, err := json.Marshal(map[string][]Heartbeat{"heartbeats": heartbeats})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.ServerURL+"/heartbeats", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+settings.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("sync failed: server returned %s", resp.Status)
	}

	return nil
}

func (a *Agent) markSynced() {
	now := time.Now().UTC()
	a.mu.Lock()
	a.lastSyncAt = &now
	a.lastError = ""
	a.mu.Unlock()
}

func (a *Agent) setError(err error) {
	a.mu.Lock()
	a.lastError = err.Error()
	a.mu.Unlock()
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
