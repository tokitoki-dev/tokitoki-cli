package kimi

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Kimi smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		shared.WriteFile(t, filepath.Join(dir, "config.json"), `{"model":"kimi-k2"}`)
		path := filepath.Join(dir, "sessions", "group", "session-a", "wire.jsonl")
		shared.WriteFile(t, path,
			`{"type":"metadata","protocol_version":"1.3"}`+"\n"+
				`{"timestamp":1770983427.123,"message":{"type":"StatusUpdate","payload":{"token_usage":{"input_other":100,"output":50,"input_cache_read":10,"input_cache_creation":20},"message_id":"msg-1"}}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderKimi,
		Model:     "kimi-k2",
		SessionID: "session-a",
		Project:   "kimi",
		Tokens: usage.TokenUsage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 20,
			CacheReadInputTokens:     10,
			TotalTokens:              180,
		},
	})
}
