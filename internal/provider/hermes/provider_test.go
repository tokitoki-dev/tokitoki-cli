package hermes

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Hermes Agent smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "state.db")
		db := providertest.OpenTestSQLite(t, dbPath)
		defer db.Close()
		providertest.ExecSQL(t, db, `CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			model TEXT,
			billing_provider TEXT,
			started_at REAL,
			message_count INTEGER,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cache_read_tokens INTEGER,
			cache_write_tokens INTEGER,
			reasoning_tokens INTEGER,
			estimated_cost_usd REAL,
			actual_cost_usd REAL
		)`)
		providertest.ExecSQL(t, db, `INSERT INTO sessions (
			id, model, billing_provider, started_at, message_count, input_tokens,
			output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			estimated_cost_usd, actual_cost_usd
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"session-a", "gpt-5.5", "openai", 1750000000.25, 42, 100, 50, 10, 20, 5, 0.12, 0.34,
		)
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderHermes,
		Model:     "gpt-5.5",
		SessionID: "session-a",
		Project:   "hermes",
		Tokens: usage.TokenUsage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 20,
			CacheReadInputTokens:     10,
			ReasoningOutputTokens:    5,
			TotalTokens:              185,
		},
	})
}
