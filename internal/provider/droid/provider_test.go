package droid

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestLoadsEntry is the Droid smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session-a.settings.json")
		providertest.WriteFile(t, path, `{"model":"Claude-Sonnet-4-[Anthropic]","providerLock":"anthropic","providerLockTimestamp":"2026-01-02T00:00:00.000Z","tokenUsage":{"inputTokens":100,"outputTokens":50,"cacheCreationTokens":20,"cacheReadTokens":10,"thinkingTokens":5}}`)
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		RunningTotal: true,
		Provider:     usage.ProviderDroid,
		Model:        "claude-sonnet-4",
		SessionID:    "session-a",
		Project:      "droid",
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

// TestReportsSidecarCWD: the launch cwd in the sibling transcript's session
// record becomes ProjectPath, under either record type Droid has written.
// A session without one keeps the old identity.
func TestReportsSidecarCWD(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "-Users-dev-workspace-demo")
	settings := `{"model":"claude-sonnet-4-5","providerLock":"anthropic","providerLockTimestamp":"%s","tokenUsage":{"inputTokens":100,"outputTokens":50}}`
	providertest.WriteFile(t, filepath.Join(dir, "sess-a.settings.json"), fmt.Sprintf(settings, "2026-01-01T00:00:00.000Z"))
	providertest.WriteFile(t, filepath.Join(dir, "sess-a.jsonl"),
		`{"type":"session_start","id":"sess-a","title":"Fix the parser","cwd":"/Users/dev/workspace/demo"}`+"\n"+
			`{"type":"message","id":"m1","message":{"role":"user","content":[{"type":"text","text":"Fix the parser"}]}}`+"\n")
	providertest.WriteFile(t, filepath.Join(dir, "sess-b.settings.json"), fmt.Sprintf(settings, "2026-01-02T00:00:00.000Z"))
	providertest.WriteFile(t, filepath.Join(dir, "sess-b.jsonl"),
		`{"type":"session","id":"sess-b","timestamp":"2026-01-02T00:00:00Z","cwd":"/Users/dev/workspace/other"}`+"\n")
	providertest.WriteFile(t, filepath.Join(dir, "sess-c.settings.json"), fmt.Sprintf(settings, "2026-01-03T00:00:00.000Z"))
	providertest.WriteFile(t, filepath.Join(dir, "sess-c.jsonl"), `{"type":"session_start","id":"sess-c"}`+"\n")
	providertest.WriteFile(t, filepath.Join(dir, "sess-d.settings.json"), fmt.Sprintf(settings, "2026-01-04T00:00:00.000Z"))

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries = %d, err = %v, want 4", len(entries), err)
	}
	want := map[string]string{
		"sess-a": "/Users/dev/workspace/demo",
		"sess-b": "/Users/dev/workspace/other",
		"sess-c": "Droid", // session record without a cwd
		"sess-d": "Droid", // no transcript at all
	}
	for _, entry := range entries {
		if entry.ProjectPath != want[entry.SessionID] {
			t.Fatalf("%s project path = %q, want %q", entry.SessionID, entry.ProjectPath, want[entry.SessionID])
		}
		if entry.Project != "droid" {
			t.Fatalf("%s project = %q, want the unchanged fallback %q", entry.SessionID, entry.Project, "droid")
		}
	}
}
