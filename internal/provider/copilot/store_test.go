package copilot

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func writeStoreFixture(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "session-store.db")
	db := providertest.OpenTestSQLite(t, dbPath)
	defer db.Close()

	statements := []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			cwd TEXT,
			repository TEXT,
			host_type TEXT,
			branch TEXT,
			summary TEXT,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE assistant_usage_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			turn_index INTEGER,
			agent_id TEXT,
			parent_tool_call_id TEXT,
			model TEXT NOT NULL,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cache_read_tokens INTEGER,
			cache_write_tokens INTEGER,
			reasoning_tokens INTEGER,
			token_details_json TEXT,
			created_at TEXT
		)`,
		`INSERT INTO sessions (id, cwd, repository, branch) VALUES
			('session-1', '/home/dev/widget', 'dev/widget', 'main')`,
		`INSERT INTO assistant_usage_events
			(session_id, turn_index, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, token_details_json, created_at)
			VALUES ('session-1', 0, 'mai-code-1-flash', 30483, 289, 7296, 0, 64,
				'[{"tokenCount":23187,"tokenType":"input"},{"tokenCount":7296,"tokenType":"cache_read"},{"tokenCount":289,"tokenType":"output"}]',
				'2026-08-04T01:19:49.344Z')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return dbPath
}

// TestLoadsStoreEntry proves the session-store database produces an entry
// with disjoint token buckets and the project taken from the sessions table.
func TestLoadsStoreEntry(t *testing.T) {
	dir := t.TempDir()
	writeStoreFixture(t, dir)

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderCopilot,
		Model:     "mai-code-1-flash",
		SessionID: "session-1",
		Project:   "widget",
		Tokens: usage.TokenUsage{
			InputTokens:           23187,
			OutputTokens:          225,
			CacheReadInputTokens:  7296,
			ReasoningOutputTokens: 64,
			TotalTokens:           30772,
		},
	})
	if entries[0].Branch != "main" {
		t.Fatalf("branch = %q, want main", entries[0].Branch)
	}
	if entries[0].ProjectPath != "/home/dev/widget" {
		t.Fatalf("project path = %q, want /home/dev/widget", entries[0].ProjectPath)
	}
}

// TestStoreEntryFileChanges proves a turn's write telemetry from the session
// transcript lands on that turn's usage entry.
func TestStoreEntryFileChanges(t *testing.T) {
	dir := t.TempDir()
	writeStoreFixture(t, dir)
	events := filepath.Join(dir, "session-state", "session-1", "events.jsonl")
	providertest.WriteFile(t, events,
		`{"type":"session.start","data":{"sessionId":"session-1","copilotVersion":"1.0.73","context":{"cwd":"/home/dev/widget","gitRoot":"/home/dev/widget","branch":"main"}},"timestamp":"2026-08-04T01:19:38.035Z"}`+"\n"+
			`{"type":"tool.execution_start","data":{"toolCallId":"call-1","toolName":"edit","turnId":"0","arguments":{"path":"/home/dev/widget/main.go"}},"timestamp":"2026-08-04T01:19:40.000Z"}`+"\n"+
			`{"type":"tool.execution_complete","data":{"toolCallId":"call-1","toolName":"edit","turnId":"0","success":true,"toolTelemetry":{"properties":{},"restrictedProperties":{"filePaths":"[\"/home/dev/widget/main.go\"]"},"metrics":{"linesAdded":12,"linesRemoved":3}}},"timestamp":"2026-08-04T01:19:41.000Z"}`+"\n")

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.LinesAdded != 12 || entry.LinesRemoved != 3 {
		t.Fatalf("lines = +%d/-%d, want +12/-3", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Entity != "/home/dev/widget/main.go" || entry.EntityType != "file" {
		t.Fatalf("entity = %q (%q), want the modified file", entry.Entity, entry.EntityType)
	}
	if entry.IsWrite == nil || !*entry.IsWrite {
		t.Fatal("IsWrite not set")
	}
	if entry.Language != "Go" {
		t.Fatalf("language = %q, want Go", entry.Language)
	}
}

// TestOTelEntryDroppedWhenStoreRecordsSameCall proves a CLI writing both
// sources does not get billed twice for one API call.
func TestOTelEntryDroppedWhenStoreRecordsSameCall(t *testing.T) {
	dir := t.TempDir()
	writeStoreFixture(t, dir)
	// Same session, model, token counts and near-identical time as the store
	// row, in OpenTelemetry form: input 30483 with 7296 cached reads, output
	// 289 with 64 reasoning tokens inside.
	providertest.WriteFile(t, filepath.Join(dir, "otel", "copilot.jsonl"),
		`{"type":"span","traceId":"trace-1","spanId":"span-1","name":"chat mai-code-1-flash","endTime":[1785806389,344000000],"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"mai-code-1-flash","gen_ai.conversation.id":"session-1","gen_ai.usage.input_tokens":30483,"gen_ai.usage.output_tokens":225,"gen_ai.usage.cache_read.input_tokens":7296,"gen_ai.usage.reasoning.output_tokens":64}}`+"\n"+
			`{"type":"span","traceId":"trace-2","spanId":"span-2","name":"chat mai-code-1-flash","endTime":[1785900000,0],"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"mai-code-1-flash","gen_ai.conversation.id":"session-2","gen_ai.usage.input_tokens":100,"gen_ai.usage.output_tokens":50}}`+"\n")

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (store row + unrelated otel span): %#v", len(entries), entries)
	}
	sessions := map[string]bool{}
	for _, entry := range entries {
		sessions[entry.SessionID] = true
	}
	if !sessions["session-1"] || !sessions["session-2"] {
		t.Fatalf("sessions = %v, want session-1 (store) and session-2 (otel)", sessions)
	}
}
