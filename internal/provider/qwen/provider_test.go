package qwen

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// TestLoadsEntry is the Qwen smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "projects", "project-a", "chats", "chat-a.jsonl")
		providertest.WriteFile(t, path, `{"type":"assistant","timestamp":"2026-01-02T00:00:00.000Z","sessionId":"session-a","model":"qwen3-coder","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":5,"cachedContentTokenCount":3,"totalTokenCount":38}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderQwen,
		Model:     "qwen3-coder",
		SessionID: "session-a",
		Project:   "qwen",
		Tokens: usage.TokenUsage{
			InputTokens:           10,
			OutputTokens:          20,
			CacheReadInputTokens:  3,
			ReasoningOutputTokens: 5,
			TotalTokens:           38,
		},
	})
}

// TestSubtractsCachedFromInclusivePrompt covers the real Google-API shape:
// totalTokenCount == prompt+candidates+thoughts, proving promptTokenCount
// already contains the cached tokens. The overlap must come out of input or
// it bills twice — once at the input rate and once at the cache-read rate.
func TestSubtractsCachedFromInclusivePrompt(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "projects", "project-a", "chats", "chat-a.jsonl")
		providertest.WriteFile(t, path, `{"type":"assistant","timestamp":"2026-01-02T00:00:00.000Z","sessionId":"session-a","model":"qwen3-coder","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":5,"cachedContentTokenCount":3,"totalTokenCount":35}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderQwen,
		Model:     "qwen3-coder",
		SessionID: "session-a",
		Project:   "qwen",
		Tokens: usage.TokenUsage{
			InputTokens:           7,
			OutputTokens:          20,
			CacheReadInputTokens:  3,
			ReasoningOutputTokens: 5,
			TotalTokens:           35,
		},
	})
}

// TestReportsRecordCWD: each record's own cwd becomes its ProjectPath, a
// record without one keeps the old directory name, and every ID is the
// record's own uuid, whatever its cwd.
func TestReportsRecordCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "-Users-dev-workspace-demo", "chats", "426ee865.jsonl")
	providertest.WriteFile(t, path,
		`{"uuid":"u1","parentUuid":null,"sessionId":"426ee865","timestamp":"2026-05-30T12:00:00.000Z","type":"assistant","cwd":"/Users/dev/workspace/demo","version":"0.17.0","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"text":"done"}]},"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`+"\n"+
			`{"uuid":"u2","parentUuid":"u1","sessionId":"426ee865","timestamp":"2026-05-30T12:00:01.000Z","type":"assistant","cwd":"/Users/dev/workspace/demo/sub","version":"0.17.0","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"text":"done"}]},"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`+"\n"+
			`{"uuid":"u3","parentUuid":"u2","sessionId":"426ee865","timestamp":"2026-05-30T12:00:02.000Z","type":"assistant","version":"0.17.0","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"text":"done"}]},"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`+"\n")

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %d, err = %v, want 3", len(entries), err)
	}
	// Newest first: u3, u2, u1.
	want := []string{"-Users-dev-workspace-demo", "/Users/dev/workspace/demo/sub", "/Users/dev/workspace/demo"}
	uuids := []string{"u3", "u2", "u1"}
	for i, entry := range entries {
		if entry.ProjectPath != want[i] {
			t.Fatalf("entry %d project path = %q, want %q", i, entry.ProjectPath, want[i])
		}
		if entry.Project != "qwen" {
			t.Fatalf("entry %d project = %q, want the unchanged fallback %q", i, entry.Project, "qwen")
		}
		if id := usageprovider.EventID(usage.ProviderQwen, entry.SessionID, uuids[i]); entry.ID != id {
			t.Fatalf("entry %d ID = %s, want its uuid's %s", i, entry.ID, id)
		}
	}
}
