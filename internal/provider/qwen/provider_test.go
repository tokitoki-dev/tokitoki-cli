package qwen

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Qwen smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "projects", "project-a", "chats", "chat-a.jsonl")
		shared.WriteFile(t, path, `{"type":"assistant","timestamp":"2026-01-02T00:00:00.000Z","sessionId":"session-a","model":"qwen3-coder","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":5,"cachedContentTokenCount":3,"totalTokenCount":38}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderQwen,
		Model:     "qwen3-coder",
		SessionID: "session-a",
		Project:   "qwen",
		Tokens: usage.TokenUsage{
			InputTokens:           10,
			OutputTokens:          20,
			CacheReadInputTokens:  3,
			ReasoningOutputTokens: 5,
			TotalTokens:           38,
		},
	})
}
