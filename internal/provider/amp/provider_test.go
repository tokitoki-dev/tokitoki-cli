package amp

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Amp smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "threads", "thread.json")
		shared.WriteFile(t, path, `{"id":"thread-a","usageLedger":{"events":[{"id":"event-a","timestamp":"2026-01-02T00:00:00.000Z","model":"gpt-5","tokens":{"input":1,"output":2}}]}}`)
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	shared.AssertSingleEntry(t, entries, err, shared.WantEntry{
		Provider:  usage.ProviderAmp,
		Model:     "gpt-5",
		SessionID: "thread-a",
		Project:   "amp",
		Tokens: usage.TokenUsage{
			InputTokens:  1,
			OutputTokens: 2,
			TotalTokens:  3,
		},
	})
}
