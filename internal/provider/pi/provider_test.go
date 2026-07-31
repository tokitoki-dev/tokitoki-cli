package pi

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the pi-agent smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "project-a", "agent_session-a.jsonl")
		shared.WriteFile(t, path, `{"type":"message","timestamp":"2026-01-02T00:00:00.000Z","message":{"role":"assistant","model":"gpt-5","usage":{"totalTokens":333}}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderPi,
		Model:     "[pi] gpt-5",
		SessionID: "session-a",
		Project:   usage.UnknownProject,
		Tokens: usage.TokenUsage{
			OutputTokens: 333,
			TotalTokens:  333,
		},
	})
}
