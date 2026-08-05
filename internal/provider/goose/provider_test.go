package goose

import (
	"path/filepath"
	"testing"

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
		Provider:  usage.ProviderGoose,
		Model:     "claude-sonnet-4-20250514",
		SessionID: "session-a",
		Project:   "goose",
		Tokens: usage.TokenUsage{
			InputTokens:           100,
			OutputTokens:          50,
			ReasoningOutputTokens: 30,
			TotalTokens:           180,
		},
	})
}
