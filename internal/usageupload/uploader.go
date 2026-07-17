package usageupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
)

const (
	DefaultServerURL = "http://localhost:9093"
	BaseURLEnv       = "TOKITOKI_BASE_URL"
)

const (
	// queueBatchSize is the number of events per upload request. It must stay
	// at or below the server's per-batch limit (lib/ingest.ts
	// MAX_BATCH_EVENTS = 5000); one batch is one server-side transaction.
	queueBatchSize = 1000

	// maxEventsPerRun bounds one sync run so it finishes inside the caller's
	// upload timeout. Whatever is left stays queued for the next run.
	maxEventsPerRun = 5000

	// uploadedRetention is how long uploaded events are kept before pruning.
	uploadedRetention = 30 * 24 * time.Hour
)

type Payload struct {
	BatchID string        `json:"batch_id"`
	Device  DevicePayload `json:"device"`
	Events  []Event       `json:"events"`
}

type DevicePayload struct {
	InstallationID string `json:"installation_id"`
	Name           string `json:"name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
}

type Event struct {
	ID                       string         `json:"id"`
	Provider                 string         `json:"provider"`
	SourceType               string         `json:"source_type,omitempty"`
	SourceProvider           string         `json:"source_provider,omitempty"`
	EventKind                string         `json:"event_kind,omitempty"`
	Timestamp                string         `json:"timestamp"`
	SessionID                string         `json:"session_id,omitempty"`
	Project                  string         `json:"project"`
	ProjectPathHash          string         `json:"project_path_hash,omitempty"`
	Model                    string         `json:"model,omitempty"`
	Language                 string         `json:"language"`
	OS                       string         `json:"os,omitempty"`
	Client                   string         `json:"client,omitempty"`
	Entity                   string         `json:"entity,omitempty"`
	EntityType               string         `json:"entity_type,omitempty"`
	Branch                   string         `json:"branch,omitempty"`
	Editor                   string         `json:"editor,omitempty"`
	Category                 string         `json:"category,omitempty"`
	IsWrite                  *bool          `json:"is_write,omitempty"`
	Raw                      map[string]any `json:"raw,omitempty"`
	InputTokens              uint64         `json:"input_tokens,omitempty"`
	OutputTokens             uint64         `json:"output_tokens,omitempty"`
	CachedInputTokens        uint64         `json:"cached_input_tokens,omitempty"`
	CacheCreationInputTokens uint64         `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64         `json:"cache_read_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64         `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64         `json:"total_tokens,omitempty"`
}

type Response struct {
	OK        bool     `json:"ok"`
	BatchID   string   `json:"batch_id"`
	Accepted  []string `json:"accepted"`
	Duplicate []string `json:"duplicate"`
	Rejected  []Reject `json:"rejected"`
}

type Reject struct {
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

// Upload sends one batch of events to the server. Callers are expected to
// keep batches at or below queueBatchSize.
func Upload(ctx context.Context, settings agent.Settings, events []usage.Entry) (Response, error) {
	if len(events) == 0 {
		return Response{OK: true}, nil
	}
	return uploadBatch(ctx, settings, events)
}

// SyncPending uploads events queued in db, oldest first, in batches. The
// first failed request stops the run: offline means every later batch fails
// the same way, and the failed events back off in the queue instead of being
// retried immediately. Uploaded events older than uploadedRetention are
// pruned before sending.
func SyncPending(ctx context.Context, settings agent.Settings, db *usagedb.DB) error {
	if _, err := db.PruneUploaded(time.Now().Add(-uploadedRetention)); err != nil {
		return err
	}

	sent := 0
	for sent < maxEventsPerRun {
		events, err := db.PendingEvents(time.Now(), queueBatchSize)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}

		response, err := uploadBatch(ctx, settings, events)
		if err != nil {
			if markErr := db.MarkEventsUploadFailed(eventIDs(events), err.Error()); markErr != nil {
				return errors.Join(err, markErr)
			}
			return err
		}

		uploaded := append(append([]string{}, response.Accepted...), response.Duplicate...)
		if err := db.MarkEventsUploaded(uploaded); err != nil {
			return err
		}
		rejected := make(map[string]string, len(response.Rejected))
		for _, item := range response.Rejected {
			if item.ID != "" {
				rejected[item.ID] = item.Reason
			}
		}
		if err := db.MarkEventsRejected(rejected); err != nil {
			return err
		}
		if len(uploaded)+len(rejected) == 0 {
			return fmt.Errorf("usage upload made no progress: server acknowledged none of %d events", len(events))
		}
		sent += len(events)
	}
	return nil
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

func uploadBatch(ctx context.Context, settings agent.Settings, events []usage.Entry) (Response, error) {
	payload := Payload{
		BatchID: "usage-" + time.Now().UTC().Format("20060102T150405.000000000Z"),
		Device: DevicePayload{
			// The server keys device rows on installation_id; an empty one
			// (possible only for callers that hand-build Settings) falls back
			// to a shared identity server-side.
			InstallationID: settings.InstallationID,
			Name:           deviceName(),
			Platform:       usage.NormalizeOS(runtime.GOOS),
			AppVersion:     buildinfo.Resolved(),
		},
		Events: make([]Event, 0, len(events)),
	}
	for _, entry := range events {
		payload.Events = append(payload.Events, convertEvent(entry))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadEndpoint(), bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if settings.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+settings.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, fmt.Errorf("usage upload failed: server returned %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var decoded Response
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Response{}, err
	}
	return decoded, nil
}

func uploadEndpoint() string {
	return BaseURL() + "/api/usage-events/batch"
}

// BaseURL is the TokiToki server every subsystem talks to — usage uploads and
// update checks alike. TOKITOKI_BASE_URL overrides the default.
func BaseURL() string {
	value := strings.TrimRight(strings.TrimSpace(os.Getenv(BaseURLEnv)), "/")
	if value == "" {
		return DefaultServerURL
	}
	return value
}

func convertEvent(entry usage.Entry) Event {
	return Event{
		ID:                       entry.ID,
		Provider:                 string(entry.Provider),
		SourceType:               entry.SourceType,
		SourceProvider:           string(entry.Provider),
		EventKind:                entry.EventKind,
		Timestamp:                entry.Timestamp.UTC().Format(time.RFC3339Nano),
		SessionID:                entry.SessionID,
		Project:                  entry.Project,
		ProjectPathHash:          hashProjectPath(entry.ProjectPath),
		Model:                    entry.Model,
		Language:                 usage.NormalizeLanguage(entry.Language),
		OS:                       entry.OS,
		Client:                   entry.Client,
		Entity:                   entry.Entity,
		EntityType:               entry.EntityType,
		Branch:                   entry.Branch,
		Editor:                   entry.Editor,
		Category:                 entry.Category,
		IsWrite:                  entry.IsWrite,
		Raw:                      entry.Raw,
		InputTokens:              entry.Usage.InputTokens,
		OutputTokens:             entry.Usage.OutputTokens,
		CachedInputTokens:        entry.Usage.CachedInputTokens,
		CacheCreationInputTokens: entry.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     entry.Usage.CacheReadInputTokens,
		ReasoningOutputTokens:    entry.Usage.ReasoningOutputTokens,
		TotalTokens:              entry.Usage.TotalTokens,
	}
}

// deviceName labels this device in the dashboard. The hostname is what users
// already call the machine; it only ever travels to their own server.
func deviceName() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "tokitoki-cli"
	}
	return strings.TrimSpace(name)
}

func hashProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}
