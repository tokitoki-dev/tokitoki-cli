package usageprovider

import (
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func TestSubtractCachedOverlap(t *testing.T) {
	cases := []struct {
		name                          string
		input, output, thoughts, tool uint64
		cached, total                 uint64
		hasTotal                      bool
		wantInput, wantCacheRead      uint64
	}{
		// total == input+output+thoughts proves the prompt count contained
		// the cached tokens: subtract the overlap.
		{"prompt includes cached", 10, 20, 5, 0, 3, 35, true, 7, 3},
		// total == the exclusive sum proves it did not: face value.
		{"cached counted separately", 10, 20, 5, 0, 3, 38, true, 10, 3},
		// No total to disambiguate with: face value.
		{"no total", 10, 20, 5, 0, 3, 0, false, 10, 3},
		// Corrupt data: cached larger than input never underflows.
		{"cached exceeds input", 2, 20, 5, 0, 9, 27, true, 0, 9},
		{"no cached", 10, 20, 5, 0, 0, 35, true, 10, 0},
	}
	for _, c := range cases {
		input, cacheRead := SubtractCachedOverlap(
			c.input, c.output, c.thoughts, c.tool, c.cached, c.total, c.hasTotal)
		if input != c.wantInput || cacheRead != c.wantCacheRead {
			t.Errorf("%s: got (%d, %d), want (%d, %d)",
				c.name, input, cacheRead, c.wantInput, c.wantCacheRead)
		}
	}
}

func TestApplyTotalFallbackKeepsResidualOutOfReasoning(t *testing.T) {
	tokens := usage.TokenUsage{InputTokens: 10, OutputTokens: 20}
	got := ApplyTotalFallback(tokens, 38)
	if got.ReasoningOutputTokens != 0 {
		t.Fatalf("residual guessed into reasoning: %+v", got)
	}
	if got.TotalTokens != 38 {
		t.Fatalf("total = %d, want 38 (residual stays as unclassified volume)", got.TotalTokens)
	}
	// A total that only confirms the sum still lands in TotalTokens.
	confirmed := ApplyTotalFallback(usage.TokenUsage{InputTokens: 10, OutputTokens: 20}, 30)
	if confirmed.TotalTokens != 30 || confirmed.ReasoningOutputTokens != 0 {
		t.Fatalf("confirming total mishandled: %+v", confirmed)
	}
}
