package workbuddy

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the WorkBuddy smoke test: a minimal fixture shaped like a
// real function_call record must produce exactly one entry with the expected
// identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "projects", "Users-eren-workspace-eapil-eye", "session-a.jsonl")
		providertest.WriteFile(t, path, `{"id":"rec-1","timestamp":1785730645570,"type":"function_call","providerData":{"messageId":"msg-1","model":"glm-5.2","requestModelId":"auto","rawUsage":{"prompt_tokens":30361,"completion_tokens":118,"total_tokens":30479,"completion_tokens_details":{"reasoning_tokens":47},"prompt_tokens_details":{"cached_tokens":14720},"prompt_cache_hit_tokens":14720,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"sessionId":"session-a","cwd":"/Users/eren/workspace/eapil-eye"}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderWorkbuddy,
		Model:     "glm-5.2",
		SessionID: "session-a",
		Project:   "eapil-eye",
		Tokens: usage.TokenUsage{
			InputTokens:           15641,
			OutputTokens:          71,
			CacheReadInputTokens:  14720,
			ReasoningOutputTokens: 47,
			TotalTokens:           30479,
		},
	})
}
