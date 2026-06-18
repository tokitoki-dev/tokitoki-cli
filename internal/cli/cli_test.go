package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/store"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
)

func TestStatusReportsIndexedEventsAndConfig(t *testing.T) {
	app, out := newApp(t, true)

	apiKey := "tokitoki_key"
	serverURL := "http://127.0.0.1:9093"
	if err := app.ConfigSet(&apiKey, &serverURL); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := app.Status(); err != nil {
		t.Fatal(err)
	}

	var status struct {
		IndexedEvents int    `json:"indexed_events"`
		ServerURL     string `json:"server_url"`
		HasAPIKey     bool   `json:"has_api_key"`
		Sources       []struct {
			Provider string `json:"provider"`
		} `json:"sources"`
	}
	decode(t, out, &status)
	if status.IndexedEvents != 0 {
		t.Fatalf("indexed_events = %d, want 0", status.IndexedEvents)
	}
	if status.ServerURL != serverURL {
		t.Fatalf("server_url = %q, want %q", status.ServerURL, serverURL)
	}
	if !status.HasAPIKey {
		t.Fatal("has_api_key = false, want true")
	}
	if len(status.Sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(status.Sources))
	}
}

func TestClaudeDailyReturnsProjectDateTokens(t *testing.T) {
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

	app, out := newApp(t, false)
	if err := app.ClaudeDaily(""); err != nil {
		t.Fatal(err)
	}

	var response struct {
		Data []struct {
			Date        string `json:"date"`
			Project     string `json:"project"`
			TotalTokens uint64 `json:"total_tokens"`
		} `json:"data"`
	}
	decode(t, out, &response)
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

func TestScanThenDailyReturnsClaudeAndCodexSummaries(t *testing.T) {
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
			`{"timestamp":"2026-05-21T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tokitoki"}}`+"\n"+
				`{"timestamp":"2026-05-21T01:02:04Z","type":"turn_context","payload":{"cwd":"/Users/me/workspace/tokitoki","model":"gpt-5.2-codex"}}`+"\n"+
				`{"timestamp":"2026-05-21T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":8,"output_tokens":4,"reasoning_output_tokens":1,"total_tokens":24}}}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_CONFIG_DIR", codexDir)

	app, out := newApp(t, true)
	if err := app.Scan(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := app.Daily("all", ""); err != nil {
		t.Fatal(err)
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
	decode(t, out, &response)
	if len(response.Sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(response.Sources))
	}
	if len(response.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(response.Data))
	}
	got := map[string]uint64{}
	for _, row := range response.Data {
		got[row.Provider+":"+row.Project] = row.TotalTokens
	}
	if got["claude:project-a"] != 15 {
		t.Fatalf("claude tokens = %d, want 15", got["claude:project-a"])
	}
	if got["codex:tokitoki"] != 24 {
		t.Fatalf("codex tokens = %d, want 24", got["codex:tokitoki"])
	}
}

func TestConfigSetThenGetRoundTrips(t *testing.T) {
	app, out := newApp(t, false)

	apiKey := "tokitoki_key"
	serverURL := "http://127.0.0.1:9093"
	if err := app.ConfigSet(&apiKey, &serverURL); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := app.ConfigGet(); err != nil {
		t.Fatal(err)
	}

	var settings agent.Settings
	decode(t, out, &settings)
	if settings.APIKey != apiKey || settings.ServerURL != serverURL {
		t.Fatalf("settings = %#v, want api_key=%q server_url=%q", settings, apiKey, serverURL)
	}
}

func newApp(t *testing.T, withDB bool) (*App, *bytes.Buffer) {
	t.Helper()

	fileStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	out := &bytes.Buffer{}
	app := &App{
		Agent: agent.New(fileStore, logger),
		Out:   out,
	}

	if withDB {
		usageDB, err := usagedb.Open(t.TempDir() + "/usage.bolt")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = usageDB.Close() })
		app.UsageDB = usageDB
		app.Scanner = usagescan.New(usageDB)
	}

	return app, out
}

func decode(t *testing.T, out *bytes.Buffer, v any) {
	t.Helper()
	if err := json.Unmarshal(out.Bytes(), v); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
}
