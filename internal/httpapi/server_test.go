package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/store"
)

func TestHeartbeatRequiresToken(t *testing.T) {
	router, _ := testRouter(t)

	body := bytes.NewBufferString(`{"entity":"/tmp/main.go","type":"file"}`)
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", body)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHeartbeatQueuesEvent(t *testing.T) {
	router, token := testRouter(t)

	payload := agent.Heartbeat{
		Time:     time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC),
		Entity:   "/tmp/main.go",
		Project:  "tracklm",
		Language: "Go",
		Editor:   "VSCode",
		Type:     "file",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/heartbeat", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	req = httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var status agent.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.QueueSize != 1 {
		t.Fatalf("queue size = %d, want 1", status.QueueSize)
	}
}

func TestClaudeDailyUsageReturnsProjectDateTokens(t *testing.T) {
	dir := t.TempDir()
	claudeDir := dir + "/claude"
	projectDir := claudeDir + "/projects/project-a/session-a"
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		projectDir+"/chat.jsonl",
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	router, token := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/claude/usage/daily", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Data []struct {
			Date        string `json:"date"`
			Project     string `json:"project"`
			TotalTokens uint64 `json:"total_tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(response.Data))
	}
	if response.Data[0].Project != "project-a" {
		t.Fatalf("project = %q, want project-a", response.Data[0].Project)
	}
	if response.Data[0].TotalTokens != 20 {
		t.Fatalf("total tokens = %d, want 20", response.Data[0].TotalTokens)
	}
}

func testRouter(t *testing.T) (http.Handler, string) {
	t.Helper()

	fileStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	token, err := fileStore.Token()
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(agent.New(fileStore, logger), logger)

	return server.httpServer.Handler, token
}
