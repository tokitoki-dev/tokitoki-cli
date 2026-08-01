package kilo

import (
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Kilo smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "session-a", sessionID: "/repo/demo"}},
		[]kiloRow{{
			id:        "row-1",
			sessionID: "session-a",
			created:   1767312000000,
			data:      `{"id":"msg-1","role":"assistant","providerID":"kilo","modelID":"kilo-auto/free","path":{"cwd":"/repo/demo"},"time":{"created":1767312000000},"tokens":{"input":100,"output":50,"reasoning":5,"cache":{"read":10,"write":20}}}`,
		}},
		nil,
	)
	entries, err := Provider{}.WithPaths([]string{path}).Entries()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderKilo,
		Model:     "kilo-auto/free",
		SessionID: "session-a",
		Project:   "demo",
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
