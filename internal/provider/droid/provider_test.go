package droid

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Droid smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session-a.settings.json")
		shared.WriteFile(t, path, `{"model":"Claude-Sonnet-4-[Anthropic]","providerLock":"anthropic","providerLockTimestamp":"2026-01-02T00:00:00.000Z","tokenUsage":{"inputTokens":100,"outputTokens":50,"cacheCreationTokens":20,"cacheReadTokens":10,"thinkingTokens":5}}`)
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderDroid,
		Model:     "claude-sonnet-4",
		SessionID: "session-a",
		Project:   "droid",
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
