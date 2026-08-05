package gemini

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadTestEntries(t *testing.T, root string) []usage.Entry {
	t.Helper()
	entries, err := loadEntries([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestSetCheckpointFormat replays the current session-*.jsonl layout: a
// metadata header followed by "$set" checkpoint lines that each carry the
// whole message history. Messages repeated across checkpoints must dedupe on
// id with the last version winning.
func TestSetCheckpointFormat(t *testing.T) {
	dir := t.TempDir()
	providertest.WriteFile(t, filepath.Join(dir, "shop", ".project_root"), "/ws/shop\n")
	providertest.WriteFile(t, filepath.Join(dir, "shop", "chats", "session-a.jsonl"),
		`{"sessionId":"session-a","projectHash":"hash-a","startTime":"2026-06-01T10:00:00.000Z","kind":"main"}`+"\n"+
			`{"$set":{"messages":[{"id":"m1","timestamp":"2026-06-01T10:00:10.000Z","type":"user","content":"hi"},{"id":"m2","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":50,"output":5,"total":55}}]}}`+"\n"+
			`{"$set":{"messages":[{"id":"m1","timestamp":"2026-06-01T10:00:10.000Z","type":"user","content":"hi"},{"id":"m2","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":100,"output":10,"cached":40,"thoughts":5,"total":115}},{"id":"m3","timestamp":"2026-06-01T10:01:00.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":10,"output":2,"total":12}}]}}`+"\n")

	entries := loadTestEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2: %#v", len(entries), entries)
	}
	first := entries[0]
	if first.SessionID != "session-a" {
		t.Fatalf("session id = %q, want %q", first.SessionID, "session-a")
	}
	if first.Project != "shop" || first.ProjectPath != "/ws/shop" {
		t.Fatalf("project = %q/%q, want shop//ws/shop", first.Project, first.ProjectPath)
	}
	want := usage.TokenUsage{
		InputTokens:           60,
		OutputTokens:          10,
		CacheReadInputTokens:  40,
		ReasoningOutputTokens: 5,
		TotalTokens:           115,
	}
	if first.Usage != want {
		t.Fatalf("usage = %#v, want %#v", first.Usage, want)
	}
	second := entries[1]
	if second.Usage.TotalTokens != 12 {
		t.Fatalf("second total = %d, want 12", second.Usage.TotalTokens)
	}
}

// TestMessageIDStableAcrossRewrites: session files are rewritten in place, so
// the same message must keep its entry id when its line or token counts move.
func TestMessageIDStableAcrossRewrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shop", "chats", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"sessionId":"session-a","projectHash":"hash-a"}`+"\n"+
			`{"id":"m1","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":50,"output":5,"total":55}}`+"\n")
	before := loadTestEntries(t, dir)

	providertest.WriteFile(t, path,
		`{"sessionId":"session-a","projectHash":"hash-a"}`+"\n"+
			`{"$set":{"summary":"noise"}}`+"\n"+
			`{"$set":{"messages":[{"id":"m1","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":80,"output":9,"total":89}}]}}`+"\n")
	after := loadTestEntries(t, dir)

	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("entries = %d/%d, want 1/1", len(before), len(after))
	}
	if before[0].ID != after[0].ID {
		t.Fatalf("entry id changed across rewrite: %q -> %q", before[0].ID, after[0].ID)
	}
}

// TestProjectRegistryFallback: without a .project_root file, the slug is
// resolved through <gemini-home>/projects.json, which maps workspace path to
// slug and lives next to the scanned tmp directory.
func TestProjectRegistryFallback(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tmp")
	providertest.WriteFile(t, filepath.Join(home, "projects.json"),
		`{"projects":{"/ws/deep/shop":"shop"}}`)
	providertest.WriteFile(t, filepath.Join(root, "shop", "chats", "session-a.jsonl"),
		`{"sessionId":"session-a"}`+"\n"+
			`{"id":"m1","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":50,"output":5,"total":55}}`+"\n")

	entries := loadTestEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Project != "shop" || entries[0].ProjectPath != "/ws/deep/shop" {
		t.Fatalf("project = %q/%q, want shop//ws/deep/shop", entries[0].Project, entries[0].ProjectPath)
	}
}

// TestHashSlugFallsBackToUnknown: a hash slug with no .project_root and no
// registry entry carries no project information at all.
func TestHashSlugFallsBackToUnknown(t *testing.T) {
	dir := t.TempDir()
	slug := "0ade338c48c531ca24306ff4d04bcdf7b2a2cb022b1c968ffa4749b403dde9b2"
	providertest.WriteFile(t, filepath.Join(dir, slug, "chats", "session-a.jsonl"),
		`{"id":"m1","timestamp":"2026-06-01T10:00:20.000Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":50,"output":5,"total":55}}`+"\n")

	entries := loadTestEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Project != usage.UnknownProject {
		t.Fatalf("project = %q, want %q", entries[0].Project, usage.UnknownProject)
	}
	if entries[0].ProjectPath != "" {
		t.Fatalf("project path = %q, want empty", entries[0].ProjectPath)
	}
}

// TestToolCallFileChanges: write_file and replace tool calls become per-file
// line stats, the entity points at the most-changed file, and the language
// comes from the touched paths. Failed tool calls are ignored.
func TestToolCallFileChanges(t *testing.T) {
	dir := t.TempDir()
	providertest.WriteFile(t, filepath.Join(dir, "shop", ".project_root"), "/ws/shop\n")
	providertest.WriteFile(t, filepath.Join(dir, "shop", "chats", "session-a.json"), `{
		"sessionId": "session-a",
		"projectHash": "hash-a",
		"startTime": "2026-06-01T10:00:00.000Z",
		"messages": [
			{"id": "m1", "timestamp": "2026-06-01T10:00:10.000Z", "type": "user", "content": "edit"},
			{"id": "m2", "timestamp": "2026-06-01T10:00:20.000Z", "type": "gemini", "model": "gemini-2.5-pro",
			 "tokens": {"input": 100, "output": 10, "total": 110},
			 "toolCalls": [
				{"id": "t1", "name": "replace", "status": "success",
				 "args": {"file_path": "src/main.go", "old_string": "a", "new_string": "b"},
				 "resultDisplay": {"filePath": "/ws/shop/src/main.go", "diffStat": {"model_added_lines": 7, "model_removed_lines": 3}}},
				{"id": "t2", "name": "write_file", "status": "success",
				 "args": {"file_path": "src/util.go", "content": "package main\nfunc util() {}\n"}},
				{"id": "t3", "name": "replace", "status": "error",
				 "args": {"file_path": "src/broken.go", "old_string": "x", "new_string": "y"}}
			 ]}
		]
	}`)

	entries := loadTestEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.LinesAdded != 9 || entry.LinesRemoved != 3 {
		t.Fatalf("lines = +%d/-%d, want +9/-3", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Entity != "/ws/shop/src/main.go" || entry.EntityType != "file" {
		t.Fatalf("entity = %q (%q), want /ws/shop/src/main.go (file)", entry.Entity, entry.EntityType)
	}
	if entry.IsWrite == nil || !*entry.IsWrite {
		t.Fatal("IsWrite not set")
	}
	if len(entry.Files) != 2 {
		t.Fatalf("files = %#v, want 2 entries", entry.Files)
	}
	if entry.Language != "Go" {
		t.Fatalf("language = %q, want Go", entry.Language)
	}
}

// TestLegacyStatsRecords: the older stats layout (per-model token summaries
// under stats.models) must keep loading.
func TestLegacyStatsRecords(t *testing.T) {
	dir := t.TempDir()
	providertest.WriteFile(t, filepath.Join(dir, "shop", "chats", "session-a.json"),
		`{"sessionId":"session-a","timestamp":"2026-06-01T10:00:00.000Z","stats":{"models":{"gemini-2.5-flash":{"tokens":{"prompt":200,"candidates":20,"cached":50,"total":220}}}}}`)

	entries := loadTestEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Model != "gemini-2.5-flash" {
		t.Fatalf("model = %q", entry.Model)
	}
	want := usage.TokenUsage{
		InputTokens:          150,
		OutputTokens:         20,
		CacheReadInputTokens: 50,
		TotalTokens:          220,
	}
	if entry.Usage != want {
		t.Fatalf("usage = %#v, want %#v", entry.Usage, want)
	}
}
