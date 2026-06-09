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
	"github.com/labx/tracklm-goagent/internal/usagedb"
)

func TestHeartbeatQueuesEvent(t *testing.T) {
	router := testRouter(t)

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
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	req = httptest.NewRequest(http.MethodGet, "/status", nil)
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

	router := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/claude/usage/daily", nil)
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

func TestDailyUsageReturnsClaudeAndCodexSummaries(t *testing.T) {
	dir := t.TempDir()
	claudeDir := dir + "/claude"
	claudeProjectDir := claudeDir + "/projects/project-a/session-a"
	if err := os.MkdirAll(claudeProjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		claudeProjectDir+"/chat.jsonl",
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":10,"output_tokens":5}}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	codexDir := dir + "/codex"
	codexSessionDir := codexDir + "/sessions/2026/05/21"
	if err := os.MkdirAll(codexSessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		codexSessionDir+"/rollout-session-a.jsonl",
		[]byte(
			`{"timestamp":"2026-05-21T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tracklm"}}`+"\n"+
				`{"timestamp":"2026-05-21T01:02:04Z","type":"turn_context","payload":{"cwd":"/Users/me/workspace/tracklm","model":"gpt-5.2-codex"}}`+"\n"+
				`{"timestamp":"2026-05-21T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":8,"output_tokens":4,"reasoning_output_tokens":1,"total_tokens":24}}}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_CONFIG_DIR", codexDir)

	router := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/usage/scan", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/usage/daily?provider=all", nil)
	rec = httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Data []struct {
			Provider    string `json:"provider"`
			Project     string `json:"project"`
			TotalTokens uint64 `json:"total_tokens"`
		} `json:"data"`
		Sources []struct {
			Provider string `json:"provider"`
			Error    string `json:"error"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(response.Sources))
	}
	if len(response.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2: %s", len(response.Data), rec.Body.String())
	}
	got := map[string]uint64{}
	for _, row := range response.Data {
		got[row.Provider+":"+row.Project] = row.TotalTokens
	}
	if got["claude:project-a"] != 15 {
		t.Fatalf("claude tokens = %d, want 15", got["claude:project-a"])
	}
	if got["codex:tracklm"] != 24 {
		t.Fatalf("codex tokens = %d, want 24", got["codex:tracklm"])
	}
}

func testRouter(t *testing.T) http.Handler {
	t.Helper()

	fileStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	usageDB, err := usagedb.Open(t.TempDir() + "/usage.bolt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = usageDB.Close()
	})

	server := NewServer(agent.New(fileStore, logger), usageDB, logger)

	return server.httpServer.Handler
}
