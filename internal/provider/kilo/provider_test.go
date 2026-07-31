package kilo

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Kilo smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "kilo.db")
		db := providertest.OpenTestSQLite(t, dbPath)
		defer db.Close()
		providertest.ExecSQL(t, db, "CREATE TABLE message (id TEXT, session_id TEXT, data TEXT)")
		providertest.ExecSQL(t, db, `INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`,
			"row-1",
			"session-a",
			`{"id":"msg-1","role":"assistant","providerID":"anthropic","modelID":"claude-sonnet-4-20250514","time":{"created":1767312000000},"tokens":{"input":100,"output":50,"reasoning":5,"cache":{"read":10,"write":20}}}`,
		)
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderKilo,
		Model:     "claude-sonnet-4-20250514",
		SessionID: "session-a",
		Project:   "kilo",
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
