package copilot

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
)

// TestFileFilterSkipsRejectedFiles proves the scanner can skip source files
// whose events are already ingested.
func TestFileFilterSkipsRejectedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "copilot.jsonl")
	providertest.WriteFile(t, path, `{"type":"span","traceId":"trace-1","spanId":"span-1","name":"chat claude-sonnet-4","endTime":[1775934264,967317833],"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"claude-sonnet-4","gen_ai.conversation.id":"conv-1","gen_ai.usage.input_tokens":19452,"gen_ai.usage.output_tokens":281}}}`+"\n")

	rejected := make([]string, 0)
	provider := Provider{}.WithPaths([]string{dir}).(Provider).
		WithFileFilter(func(candidate string) bool {
			rejected = append(rejected, candidate)
			return false
		})
	entries, err := provider.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0 when the filter rejects every file", len(entries))
	}
	if len(rejected) != 1 || rejected[0] != path {
		t.Fatalf("filter saw %#v, want the session file", rejected)
	}
}
