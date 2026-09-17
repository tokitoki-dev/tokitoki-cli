// Package statusbar fetches the figure an editor's status bar shows — today's
// active time and tokens for the account behind the API key — and keeps the
// last answer on disk so a status bar has something to show while offline.
package statusbar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

// CacheFile is the state file holding the last report the server gave.
const CacheFile = "today.json"

// Report is the server's answer (tracklm-nextjs lib/statusbar.ts), plus two
// fields of local provenance. The figure is computed server-side on the
// dashboard's own rule and clock, so every machine holding the same key
// shows the same number.
type Report struct {
	Date          string `json:"date"`
	Timezone      string `json:"timezone"`
	Scope         string `json:"scope"`
	TeamName      string `json:"team_name,omitempty"`
	ActiveSeconds int64  `json:"active_seconds"`
	TotalTokens   uint64 `json:"total_tokens"`
	// Text is ready to display: "3h 23m". Formatted by the server so every
	// client agrees on the spelling.
	Text string `json:"text"`
	// Project is the same day narrowed to the project that was asked for;
	// nil when none was.
	Project *ProjectReport `json:"project,omitempty"`
	// Stale marks a report served from the cache because the server could
	// not be reached: last known, not current.
	Stale     bool      `json:"stale"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ProjectReport is one project's share of the day.
type ProjectReport struct {
	Name          string `json:"name"`
	ActiveSeconds int64  `json:"active_seconds"`
	TotalTokens   uint64 `json:"total_tokens"`
	Text          string `json:"text"`
}

// ErrUnauthorized reports that the server rejected the key.
var ErrUnauthorized = errors.New("status: API key rejected by server")

// Fetch asks the server for today's figure, narrowed to project as well
// when one is named.
func Fetch(ctx context.Context, baseURL, apiKey, project string) (Report, error) {
	endpoint := baseURL + "/api/statusbar/today"
	if project != "" {
		endpoint += "?project=" + url.QueryEscape(project)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Report{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("fetch today: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return Report{}, ErrUnauthorized
	default:
		return Report{}, fmt.Errorf("fetch today: server returned %s", resp.Status)
	}

	var decoded struct {
		OK bool `json:"ok"`
		Report
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Report{}, fmt.Errorf("fetch today: %w", err)
	}
	if !decoded.OK || decoded.Date == "" {
		return Report{}, errors.New("fetch today: server response carried no report")
	}
	report := decoded.Report
	report.Stale = false
	report.FetchedAt = time.Now().UTC()
	return report, nil
}

// Save writes the report as the last known answer. Written to a sibling and
// renamed, so a reader never sees a half-written file.
func Save(dataDir string, report Report) error {
	path := store.StatePath(dataDir, CacheFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	staging := path + ".tmp"
	if err := os.WriteFile(staging, data, 0o600); err != nil {
		return err
	}
	return os.Rename(staging, path)
}

// Load returns the last saved report, marked stale. ok is false when none
// was ever saved; an unreadable file counts as none. The project share is
// kept only when it is the project asked for now: another window's project
// is not an answer about this one, while the account total is the same
// whichever window asks.
func Load(dataDir, project string) (report Report, ok bool) {
	data, err := os.ReadFile(store.StatePath(dataDir, CacheFile))
	if err != nil {
		return Report{}, false
	}
	if err := json.Unmarshal(data, &report); err != nil || report.Date == "" {
		return Report{}, false
	}
	if report.Project != nil && report.Project.Name != project {
		report.Project = nil
	}
	report.Stale = true
	return report, true
}
