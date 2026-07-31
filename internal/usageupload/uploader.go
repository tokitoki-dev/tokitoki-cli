package usageupload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
)

const (
	DefaultServerURL = "https://tokitoki.dev"
	BaseURLEnv       = "TOKITOKI_BASE_URL"
)

const (
	// queueBatchSize is the number of events per upload request. It must stay
	// at or below the server's per-batch limit (lib/ingest.ts
	// MAX_BATCH_EVENTS = 5000); one batch is one server-side transaction.
	// Larger batches reduce network round-trips and amortize HTTP overhead.
	queueBatchSize = 5000

	// uploadedRetention is how long uploaded events are kept before pruning.
	uploadedRetention = 30 * 24 * time.Hour
)

type Payload struct {
	BatchID string        `json:"batch_id"`
	Device  DevicePayload `json:"device"`
	Events  []Event       `json:"events"`
}

// DevicePayload labels which machine a batch came from. It is display metadata
// only — event identity is the content hash in Event.ID, and the server dedupes
// on (user, event id). Nothing here may affect whether an event is stored.
type DevicePayload struct {
	Name       string `json:"name,omitempty"`
	Platform   string `json:"platform,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
}

type Event struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	SourceType     string `json:"source_type,omitempty"`
	SourceProvider string `json:"source_provider,omitempty"`
	EventKind      string `json:"event_kind,omitempty"`
	Timestamp      string `json:"timestamp"`
	// The machine's IANA zone ("Asia/Tokyo"), omitted when it cannot be
	// resolved — see usage.MachineTimezone. Never a fixed abbreviation like
	// "JST": those are ambiguous across regions and cannot be re-expanded.
	//
	// The UTC offset is deliberately not sent alongside it. Timestamp is an
	// absolute instant and this is a zone, so the offset at that instant —
	// including whether DST was in effect, and half-hour zones like
	// Australia/Lord_Howe — is a pure function of the two. Sending it as well
	// would be a second copy of a derived value, and the copy is what goes
	// stale when tzdata is corrected.
	Timezone                 string             `json:"timezone,omitempty"`
	SessionID                string             `json:"session_id,omitempty"`
	Project                  string             `json:"project"`
	ProjectPathHash          string             `json:"project_path_hash,omitempty"`
	Model                    string             `json:"model,omitempty"`
	Language                 string             `json:"language"`
	OS                       string             `json:"os,omitempty"`
	Client                   string             `json:"client,omitempty"`
	Entity                   string             `json:"entity,omitempty"`
	EntityType               string             `json:"entity_type,omitempty"`
	Branch                   string             `json:"branch,omitempty"`
	Editor                   string             `json:"editor,omitempty"`
	Category                 string             `json:"category,omitempty"`
	IsWrite                  *bool              `json:"is_write,omitempty"`
	LinesAdded               uint64             `json:"lines_added,omitempty"`
	LinesRemoved             uint64             `json:"lines_removed,omitempty"`
	Files                    []usage.FileChange `json:"files,omitempty"`
	Raw                      map[string]any     `json:"raw,omitempty"`
	InputTokens              uint64             `json:"input_tokens,omitempty"`
	OutputTokens             uint64             `json:"output_tokens,omitempty"`
	CachedInputTokens        uint64             `json:"cached_input_tokens,omitempty"`
	CacheCreationInputTokens uint64             `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64             `json:"cache_read_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64             `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64             `json:"total_tokens,omitempty"`
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

// SyncPending uploads events queued in db, newest first, in batches. Priority order:
// 1. Never-attempted (pending)  - highest priority
// 2. Failed (with backoff)       - medium priority
// 3. Rejected                    - never retried (status stays rejected)
// 4. Uploaded/Duplicate          - ignored (already processed)
//
// Uploaded events older than uploadedRetention are pruned before sending.
// SyncPending continues until all pending+failed events are sent or an error occurs.
func SyncPending(ctx context.Context, settings agent.Settings, db *usagedb.DB) error {
	if _, err := db.PruneUploaded(time.Now().Add(-uploadedRetention)); err != nil {
		return err
	}

	for {
		events, err := db.PendingEvents(time.Now(), queueBatchSize)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}

		response, err := uploadBatch(ctx, settings, events)
		if err != nil {
			// Upload error (network/timeout). Mark as failed with exponential backoff.
			// Will be retried next sync.
			if markErr := db.MarkEventsUploadFailed(eventIDs(events), err.Error()); markErr != nil {
				return errors.Join(err, markErr)
			}
			return err
		}

		// Accepted: newly inserted events (never seen before). Mark as uploaded.
		if len(response.Accepted) > 0 {
			if err := db.MarkEventsUploaded(response.Accepted); err != nil {
				return err
			}
		}

		// Duplicate + Rejected: both are "acknowledged and won't be processed again".
		// Duplicates are events we already uploaded. Rejected are events that failed
		// validation. Both should stop querying; we mark them as uploaded to clear them.
		ackd := append([]string{}, response.Duplicate...)
		for _, r := range response.Rejected {
			if r.ID != "" {
				ackd = append(ackd, r.ID)
			}
		}
		if len(ackd) > 0 {
			if err := db.MarkEventsUploaded(ackd); err != nil {
				return err
			}
		}

		// Sanity check: server must acknowledge every event as accepted/duplicate/rejected.
		// If not, it's a server bug or response parsing error.
		totalAcknowledged := len(response.Accepted) + len(response.Duplicate) + len(response.Rejected)
		if totalAcknowledged != len(events) {
			return fmt.Errorf("usage upload incomplete: server acknowledged %d/%d events (accepted=%d, duplicate=%d, rejected=%d)",
				totalAcknowledged, len(events), len(response.Accepted), len(response.Duplicate), len(response.Rejected))
		}
	}
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
			Name:       deviceName(),
			Platform:   usage.NormalizeOS(runtime.GOOS),
			AppVersion: buildinfo.Resolved(),
		},
		Events: make([]Event, 0, len(events)),
	}
	// A property of the machine, not of any one event, so it is resolved once
	// per upload rather than per event.
	zoneName := usage.MachineTimezone()
	for _, entry := range events {
		payload.Events = append(payload.Events, convertEvent(entry, zoneName))
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}

	// Compress the payload with gzip to reduce network bandwidth.
	var compressedBody bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBody)
	if _, err := gzipWriter.Write(body); err != nil {
		return Response{}, fmt.Errorf("gzip compression failed: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return Response{}, fmt.Errorf("gzip close failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadEndpoint(), &compressedBody)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
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

// BaseURL is the Tokitoki server every subsystem talks to — usage uploads and
// update checks alike. TOKITOKI_BASE_URL overrides the default.
func BaseURL() string {
	value := strings.TrimRight(strings.TrimSpace(os.Getenv(BaseURLEnv)), "/")
	if value == "" {
		return DefaultServerURL
	}
	return value
}

// convertEvent maps one loaded entry onto the wire format.
//
// `zoneName` is this machine's IANA zone, or "" when it could not be resolved.
// It is passed in rather than looked up here so the lookup happens once per
// upload instead of once per event.
//
// A caveat worth stating plainly, because the field name cannot: the agent logs
// this reads store their timestamps in UTC, so the zone an event was *recorded*
// in is not recoverable from them. What is reported is the zone of the machine
// doing the reading. For the ordinary case — you code and upload on the same
// laptop — those are the same thing. For a backfill of logs copied from another
// machine, or from a machine you have since moved, they are not, and the server
// should treat this as "where the user was, approximately" rather than as an
// exact per-event fact.
func convertEvent(entry usage.Entry, zoneName string) Event {
	return Event{
		ID:                       entry.ID,
		Provider:                 string(entry.Provider),
		SourceType:               entry.SourceType,
		SourceProvider:           string(entry.Provider),
		EventKind:                entry.EventKind,
		Timestamp:                entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Timezone:                 zoneName,
		SessionID:                entry.SessionID,
		Project:                  entry.Project,
		ProjectPathHash:          hashProjectPath(entry.ProjectPath),
		Model:                    entry.Model,
		Language:                 usage.NormalizeLanguage(entry.Language),
		OS:                       entry.OS,
		Client:                   entry.Client,
		Entity:                   relativeEntity(entry.ProjectPath, entry.Entity),
		EntityType:               entry.EntityType,
		Branch:                   entry.Branch,
		Editor:                   entry.Editor,
		Category:                 entry.Category,
		IsWrite:                  entry.IsWrite,
		LinesAdded:               entry.LinesAdded,
		LinesRemoved:             entry.LinesRemoved,
		Files:                    relativeFiles(entry.ProjectPath, entry.Files),
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

// relativeEntity strips the local filesystem prefix from an entity path for
// the same reason the project path is uploaded as a hash: the server sees the
// file's place inside the project, never the machine's directory layout.
func relativeEntity(projectPath, entity string) string {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return ""
	}
	if projectPath = strings.TrimSpace(projectPath); projectPath != "" {
		if rel, err := filepath.Rel(projectPath, entity); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(entity)
}

// relativeFiles applies the same machine-layout stripping to the per-file
// breakdown that relativeEntity applies to the entity.
func relativeFiles(projectPath string, files []usage.FileChange) []usage.FileChange {
	if len(files) == 0 {
		return nil
	}
	out := make([]usage.FileChange, len(files))
	for i, file := range files {
		out[i] = file
		out[i].Path = relativeEntity(projectPath, file.Path)
	}
	return out
}
