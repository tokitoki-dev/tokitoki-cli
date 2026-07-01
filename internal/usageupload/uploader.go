package usageupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/usage"
)

const (
	DefaultServerURL = "http://localhost:9093"
	BaseURLEnv       = "TOKITOKI_BASE_URL"
)

// maxBatchEvents must stay at or below the server's per-batch limit
// (lib/ingest.ts MAX_BATCH_EVENTS = 5000). Larger uploads are split into
// several requests; one batch is one server-side transaction.
const maxBatchEvents = 5000

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
	ID                       string `json:"id"`
	Provider                 string `json:"provider"`
	Timestamp                string `json:"timestamp"`
	SessionID                string `json:"session_id,omitempty"`
	Project                  string `json:"project"`
	ProjectPathHash          string `json:"project_path_hash,omitempty"`
	Model                    string `json:"model,omitempty"`
	Language                 string `json:"language"`
	OS                       string `json:"os,omitempty"`
	Client                   string `json:"client,omitempty"`
	InputTokens              uint64 `json:"input_tokens,omitempty"`
	OutputTokens             uint64 `json:"output_tokens,omitempty"`
	CachedInputTokens        uint64 `json:"cached_input_tokens,omitempty"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64 `json:"total_tokens,omitempty"`
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

type BatchError struct {
	Events []usage.Entry
	Err    error
}

func (e BatchError) Error() string {
	return e.Err.Error()
}

func (e BatchError) Unwrap() error {
	return e.Err
}

func Upload(ctx context.Context, settings agent.Settings, events []usage.Entry) (Response, error) {
	return UploadEach(ctx, settings, events, nil)
}

func UploadEach(ctx context.Context, settings agent.Settings, events []usage.Entry, onBatch func([]usage.Entry, Response) error) (Response, error) {
	if len(events) == 0 {
		return Response{OK: true}, nil
	}

	result := Response{OK: true}
	for start := 0; start < len(events); start += maxBatchEvents {
		end := start + maxBatchEvents
		if end > len(events) {
			end = len(events)
		}
		batch := events[start:end]
		resp, err := uploadBatch(ctx, settings, batch)
		if err != nil {
			return result, BatchError{Events: batch, Err: err}
		}
		if onBatch != nil {
			if err := onBatch(batch, resp); err != nil {
				return result, err
			}
		}
		result.BatchID = resp.BatchID
		result.Accepted = append(result.Accepted, resp.Accepted...)
		result.Duplicate = append(result.Duplicate, resp.Duplicate...)
		result.Rejected = append(result.Rejected, resp.Rejected...)
	}
	return result, nil
}

func uploadBatch(ctx context.Context, settings agent.Settings, events []usage.Entry) (Response, error) {
	payload := Payload{
		BatchID: "usage-" + time.Now().UTC().Format("20060102T150405.000000000Z"),
		Device: DevicePayload{
			InstallationID: "local-go-agent",
			Name:           "TokiToki Go Agent",
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
	return baseURL() + "/api/usage-events/batch"
}

func baseURL() string {
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
		Timestamp:                entry.Timestamp.UTC().Format(time.RFC3339Nano),
		SessionID:                entry.SessionID,
		Project:                  entry.Project,
		ProjectPathHash:          hashProjectPath(entry.ProjectPath),
		Model:                    entry.Model,
		Language:                 usage.NormalizeLanguage(entry.Language),
		OS:                       entry.OS,
		Client:                   entry.Client,
		InputTokens:              entry.Usage.InputTokens,
		OutputTokens:             entry.Usage.OutputTokens,
		CachedInputTokens:        entry.Usage.CachedInputTokens,
		CacheCreationInputTokens: entry.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     entry.Usage.CacheReadInputTokens,
		ReasoningOutputTokens:    entry.Usage.ReasoningOutputTokens,
		TotalTokens:              entry.Usage.TotalTokens,
	}
}

func hashProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}
