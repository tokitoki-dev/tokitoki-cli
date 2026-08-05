package codebuff

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Codebuff smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		root := filepath.Join(t.TempDir(), "manicode")
		path := filepath.Join(root, "projects", "project-a", "chats", "2026-01-02T03-04-05.000Z", "chat-messages.json")
		providertest.WriteFile(t, path, `[{"id":"assistant-message","role":"assistant","timestamp":"2026-01-02T03:04:06.000Z","metadata":{"model":"claude-sonnet-4-20250514","usage":{"inputTokens":100,"outputTokens":50,"cacheCreationInputTokens":20,"cacheReadInputTokens":10}}}]`)
		return Provider{}.WithPaths([]string{root}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderCodebuff,
		Model:     "claude-sonnet-4-20250514",
		SessionID: "manicode/project-a/2026-01-02T03-04-05.000Z",
		Project:   "codebuff",
		Tokens: usage.TokenUsage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 20,
			CacheReadInputTokens:     10,
			TotalTokens:              180,
		},
	})
}
