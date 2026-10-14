package goose

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Goose smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "sessions.db")
		db := providertest.OpenTestSQLite(t, dbPath)
		defer db.Close()
		providertest.ExecSQL(t, db, `CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			model_config_json TEXT,
			provider_name TEXT,
			created_at TEXT,
			total_tokens INTEGER,
			input_tokens INTEGER,
			output_tokens INTEGER,
			accumulated_total_tokens INTEGER,
			accumulated_input_tokens INTEGER,
			accumulated_output_tokens INTEGER
		)`)
		providertest.ExecSQL(t, db, `INSERT INTO sessions (
			id, model_config_json, provider_name, created_at,
			accumulated_total_tokens, accumulated_input_tokens, accumulated_output_tokens
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"session-a", `{"model_name":"claude-sonnet-4-20250514"}`, "anthropic", "2026-05-01 01:02:03", 180, 100, 50,
		)
		return Provider{}.WithPaths([]string{dbPath}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		RunningTotal: true,
		Provider:     usage.ProviderGoose,
		Model:        "claude-sonnet-4-20250514",
		SessionID:    "session-a",
		Project:      "goose",
		Tokens: usage.TokenUsage{
			InputTokens:           100,
			OutputTokens:          50,
			ReasoningOutputTokens: 30,
			TotalTokens:           180,
		},
	})
}

// TestReportsWorkingDir: current Goose records the session's working_dir.
// It becomes ProjectPath, while the ID stays the one computed from the old
// "goose"/"Goose" identity — every scan re-reads the whole database, so a
// changed ID would re-upload every session.
func TestReportsWorkingDir(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db := providertest.OpenTestSQLite(t, dbPath)
	providertest.ExecSQL(t, db, `CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		session_type TEXT NOT NULL DEFAULT 'user',
		working_dir TEXT NOT NULL,
		created_at TEXT,
		total_tokens INTEGER,
		input_tokens INTEGER,
		output_tokens INTEGER,
		accumulated_total_tokens INTEGER,
		accumulated_input_tokens INTEGER,
		accumulated_output_tokens INTEGER,
		provider_name TEXT,
		model_config_json TEXT
	)`)
	providertest.ExecSQL(t, db, `INSERT INTO sessions (
		id, working_dir, created_at, total_tokens, input_tokens, output_tokens,
		provider_name, model_config_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"20260501_1", "/Users/dev/workspace/demo", "2026-05-01 01:02:03", 150, 100, 50,
		"anthropic", `{"model_name":"claude-sonnet-4-20250514","context_limit":200000}`,
	)
	db.Close()

	entries, err := Provider{}.WithPaths([]string{dbPath}).Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v, want 1", len(entries), err)
	}
	entry := entries[0]
	if entry.ProjectPath != "/Users/dev/workspace/demo" {
		t.Fatalf("project path = %q, want the working_dir", entry.ProjectPath)
	}
	if entry.Project != "goose" {
		t.Fatalf("project = %q, want the unchanged fallback %q", entry.Project, "goose")
	}
}

// Real Goose declares created_at and updated_at TIMESTAMP, which the SQLite
// driver returns as time.Time rather than text; every row used to be dropped
// as unparseable. updated_at is when the session last moved, which is when
// its growth since the previous read happened.
func TestTimestampColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db := providertest.OpenTestSQLite(t, dbPath)
	providertest.ExecSQL(t, db, `CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		working_dir TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		total_tokens INTEGER,
		input_tokens INTEGER,
		output_tokens INTEGER,
		accumulated_total_tokens INTEGER,
		accumulated_input_tokens INTEGER,
		accumulated_output_tokens INTEGER,
		provider_name TEXT,
		model_config_json TEXT
	)`)
	providertest.ExecSQL(t, db, `INSERT INTO sessions (
		id, working_dir, created_at, updated_at, total_tokens, input_tokens, output_tokens,
		provider_name, model_config_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"20260501_1", "/Users/dev/workspace/demo", "2026-05-01 01:02:03", "2026-05-02 08:00:00", 150, 100, 50,
		"anthropic", `{"model_name":"claude-sonnet-4-20250514","context_limit":200000}`,
	)
	db.Close()

	entries, err := Provider{}.WithPaths([]string{dbPath}).Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v, want 1", len(entries), err)
	}
	if got, want := entries[0].Timestamp, time.Date(2026, 5, 2, 8, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("timestamp = %s, want updated_at %s", got, want)
	}
}

// TestOldDatabaseWithoutWorkingDir: databases from before working_dir existed
// must still scan, keeping the old ProjectPath.
func TestOldDatabaseWithoutWorkingDir(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db := providertest.OpenTestSQLite(t, dbPath)
	providertest.ExecSQL(t, db, `CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		model_config_json TEXT,
		provider_name TEXT,
		created_at TEXT,
		total_tokens INTEGER,
		input_tokens INTEGER,
		output_tokens INTEGER,
		accumulated_total_tokens INTEGER,
		accumulated_input_tokens INTEGER,
		accumulated_output_tokens INTEGER
	)`)
	providertest.ExecSQL(t, db, `INSERT INTO sessions (
		id, model_config_json, provider_name, created_at,
		accumulated_total_tokens, accumulated_input_tokens, accumulated_output_tokens
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"session-a", `{"model_name":"claude-sonnet-4-20250514"}`, "anthropic", "2026-05-01 01:02:03", 180, 100, 50,
	)
	db.Close()

	entries, err := Provider{}.WithPaths([]string{dbPath}).Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v, want 1", len(entries), err)
	}
	entry := entries[0]
	if entry.ProjectPath != "Goose" {
		t.Fatalf("project path = %q, want the old %q", entry.ProjectPath, "Goose")
	}
}
