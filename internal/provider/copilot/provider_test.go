package copilot

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the GitHub Copilot CLI smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "copilot.jsonl")
		providertest.WriteFile(t, path, `{"type":"span","traceId":"trace-1","spanId":"span-1","name":"chat claude-sonnet-4","endTime":[1775934264,967317833],"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"claude-sonnet-4","gen_ai.conversation.id":"conv-1","gen_ai.usage.input_tokens":19452,"gen_ai.usage.output_tokens":281,"gen_ai.usage.cache_read.input_tokens":123,"gen_ai.usage.cache_creation.input_tokens":25,"gen_ai.usage.reasoning.output_tokens":128}}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderCopilot,
		Model:     "claude-sonnet-4",
		SessionID: "conv-1",
		Project:   "copilot",
		Tokens: usage.TokenUsage{
			InputTokens:              19329,
			OutputTokens:             281,
			CacheCreationInputTokens: 25,
			CacheReadInputTokens:     123,
			ReasoningOutputTokens:    128,
			TotalTokens:              19886,
		},
	})
}
