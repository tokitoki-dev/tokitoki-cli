package gemini

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Gemini CLI smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session-a.jsonl")
		shared.WriteFile(t, path,
			`{"sessionId":"session-a","projectHash":"project-a","startTime":"2026-05-17T11:07:00.000Z"}`+"\n"+
				`{"id":"msg-a","timestamp":"2026-05-17T11:07:32.000Z","type":"gemini","model":"gemini-3-flash-preview","tokens":{"input":15327,"output":23,"cached":11526,"thoughts":919,"tool":7,"total":16276}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderGemini,
		Model:     "gemini-3-flash-preview",
		SessionID: "session-a",
		Project:   "gemini",
		Tokens: usage.TokenUsage{
			InputTokens:           3808,
			OutputTokens:          23,
			CacheReadInputTokens:  11526,
			ReasoningOutputTokens: 919,
			TotalTokens:           16276,
		},
	})
}
