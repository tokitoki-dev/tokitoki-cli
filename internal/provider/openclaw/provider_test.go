package openclaw

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the OpenClaw smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agents", "main", "sessions", "abc.jsonl")
		providertest.WriteFile(t, path,
			`{"type":"model_change","provider":"openai-codex","modelId":"gpt-5.2"}`+"\n"+
				`{"type":"message","message":{"role":"assistant","usage":{"input":1660,"output":55,"cacheRead":108928},"timestamp":1769753935279}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderOpenClaw,
		Model:     "[openclaw] gpt-5.2",
		SessionID: "abc",
		Project:   "openclaw",
		Tokens: usage.TokenUsage{
			InputTokens:          1660,
			OutputTokens:         55,
			CacheReadInputTokens: 108928,
			TotalTokens:          110643,
		},
	})
}
