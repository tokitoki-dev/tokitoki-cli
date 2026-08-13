// Package telemetry reports that this machine has Tokitoki installed and
// whether an API key is configured. This is the only signal the server gets
// about installs that never upload anything — without it, a user who installs
// and never configures a key is invisible.
//
// The ping carries an HMAC of the OS machine id (see machineid), the
// platform, the version, and one boolean. No hostname, no paths, no events.
// While no API key is configured it is anonymous. Once a key is set, the ping
// authenticates with it — the same Bearer header every upload already sends —
// so the server may tie the install to the account it already knows from
// those uploads; this reveals nothing the uploads have not.
// TOKITOKI_NO_TELEMETRY disables it entirely.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/machineid"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const (
	pingInterval = 24 * time.Hour
	pingTimeout  = 10 * time.Second
	stampName    = "last-ping"

	// OptOutEnv disables the install ping when set to any non-empty value.
	OptOutEnv = "TOKITOKI_NO_TELEMETRY"
)

type payload struct {
	MachineID  string `json:"machine_id"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
	AppVersion string `json:"app_version"`
	HasAPIKey  bool   `json:"has_api_key"`
}

// MaybePing reports this install at most once per pingInterval across
// processes. The stamp file's mtime is the whole record — written before the
// attempt so network failures are throttled the same as successes. A missing
// stamp is a fresh install, which pings immediately.
func MaybePing(logger *slog.Logger, baseURL string) {
	dir, ok := pingDir()
	if !ok {
		return
	}
	stamp := store.StatePath(dir, stampName)
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < pingInterval {
		return
	}
	ping(logger, dir, baseURL)
}

// Ping reports this install now, ignoring the throttle, and resets it. Used
// after `set key`: the whole point of has_api_key is telling configured
// installs from abandoned ones, so the flip must not wait a day to arrive.
func Ping(logger *slog.Logger, baseURL string) {
	dir, ok := pingDir()
	if !ok {
		return
	}
	ping(logger, dir, baseURL)
}

func pingDir() (string, bool) {
	if os.Getenv(OptOutEnv) != "" {
		return "", false
	}
	dir, err := store.InitializeDataDir()
	if err != nil {
		return "", false
	}
	return dir, true
}

// ping never reports failure to the caller: telemetry that can fail a user's
// command is worse than no telemetry.
func ping(logger *slog.Logger, dir, baseURL string) {
	if err := os.WriteFile(store.StatePath(dir, stampName), nil, 0o600); err != nil {
		return
	}

	apiKey := ""
	if fileStore, err := store.Open(dir); err == nil {
		if settings, err := fileStore.LoadSettings(); err == nil {
			apiKey = settings.APIKey
		}
	}
	hasKey := apiKey != ""

	body, err := json.Marshal(payload{
		MachineID:  machineid.ID(),
		Platform:   usage.NormalizeOS(runtime.GOOS),
		Arch:       runtime.GOARCH,
		AppVersion: buildinfo.Resolved(),
		HasAPIKey:  hasKey,
	})
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/telemetry/ping", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	// Same header the uploader sends: a configured install identifies itself,
	// so the admin funnel can name who each configured machine belongs to.
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Debug("tokitoki install ping failed", "error", err)
		return
	}
	resp.Body.Close()
}
