package gemini

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Gemini CLI smoke test: a minimal fixture in the real
// ~/.gemini/tmp/<slug>/chats layout must produce exactly one entry with the
// expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		providertest.WriteFile(t, filepath.Join(dir, "shop", ".project_root"),
			filepath.Join(dir, "workspace", "shop")+"\n")
		providertest.WriteFile(t, filepath.Join(dir, "shop", "chats", "session-a.jsonl"),
			`{"sessionId":"session-a","projectHash":"project-a","startTime":"2026-05-17T11:07:00.000Z"}`+"\n"+
				`{"id":"msg-a","timestamp":"2026-05-17T11:07:32.000Z","type":"gemini","model":"gemini-3-flash-preview","tokens":{"input":15327,"output":23,"cached":11526,"thoughts":919,"tool":7,"total":16276}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderGemini,
		Model:     "gemini-3-flash-preview",
		SessionID: "session-a",
		Project:   "shop",
		Tokens: usage.TokenUsage{
			InputTokens:           3808,
			OutputTokens:          23,
			CacheReadInputTokens:  11526,
			ReasoningOutputTokens: 919,
			TotalTokens:           16276,
		},
	})
}
