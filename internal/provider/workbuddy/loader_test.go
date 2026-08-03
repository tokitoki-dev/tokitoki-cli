package workbuddy

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// TestDedupesSharedMessageID verifies that the function_call and message
// records of one LLM round-trip — which share providerData.messageId — count
// once, not twice.
func TestDedupesSharedMessageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"timestamp":1785730645570,"type":"function_call","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":100,"completion_tokens":10}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n"+
			`{"timestamp":1785730645580,"type":"message","role":"assistant","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":100,"completion_tokens":10}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (messageId must dedupe)", len(entries))
	}
	if entries[0].Usage.TotalTokens != 110 {
		t.Fatalf("total = %d, want 110", entries[0].Usage.TotalTokens)
	}
}

// TestKeepsDistinctCallsSharingMessageID verifies that two REAL API calls
// under one messageId — same assistant turn, different token counts — both
// count. Observed in real data: a Write call and its continuation share the
// messageId but each consumed its own tokens.
func TestKeepsDistinctCallsSharingMessageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"timestamp":1785730404526,"type":"function_call","name":"Write","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":37883,"completion_tokens":2466,"prompt_cache_hit_tokens":37760}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n"+
			`{"timestamp":1785730406541,"type":"function_call","name":"TaskUpdate","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":40561,"completion_tokens":77,"prompt_cache_hit_tokens":37824}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (distinct usage under one messageId must both count)", len(entries))
	}
}

// TestSubagentSessionID verifies that sub-agent transcripts nested at
// <session>/subagents/<agent>.jsonl attribute usage to the parent session.
func TestSubagentSessionID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a", "subagents", "agent-1.jsonl")
	providertest.WriteFile(t, path, `{"timestamp":1785730645570,"type":"function_call","providerData":{"messageId":"msg-sub","model":"glm-5.2","rawUsage":{"prompt_tokens":50,"completion_tokens":5}},"cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].SessionID != "session-a" {
		t.Fatalf("session id = %q, want %q", entries[0].SessionID, "session-a")
	}
}

// TestAnthropicStyleCacheSplit verifies the Anthropic-mirrored cache fields:
// prompt_tokens is the full prompt, so cache reads AND writes are both
// subtracted to get pure input.
func TestAnthropicStyleCacheSplit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path, `{"timestamp":1785730645570,"type":"message","providerData":{"messageId":"msg-1","model":"claude-x","rawUsage":{"prompt_tokens":350,"completion_tokens":20,"cache_read_input_tokens":100,"cache_creation_input_tokens":200}},"cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	want := usage.TokenUsage{
		InputTokens:              50,
		OutputTokens:             20,
		CacheCreationInputTokens: 200,
		CacheReadInputTokens:     100,
		TotalTokens:              370,
	}
	if entries[0].Usage != want {
		t.Fatalf("usage = %#v, want %#v", entries[0].Usage, want)
	}
}

// TestModelFallsBackToSettings verifies that a record naming no model uses
// the settings.json model, and "auto" as the last resort.
func TestModelFallsBackToSettings(t *testing.T) {
	dir := t.TempDir()
	providertest.WriteFile(t, filepath.Join(dir, "settings.json"), `{"model":"glm-configured"}`)
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path, `{"timestamp":1785730645570,"type":"message","providerData":{"messageId":"msg-1","rawUsage":{"prompt_tokens":10,"completion_tokens":1}},"cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Model != "glm-configured" {
		t.Fatalf("model = %q, want %q", entries[0].Model, "glm-configured")
	}
}

// TestAttachesFileChangesByMessageID verifies that a Write tool call without
// its own usage lands on the usage entry of the same assistant turn — matched
// by messageId even when the call precedes the turn's usage record — and that
// the entry gets language, entity, and line stats from it.
func TestAttachesFileChangesByMessageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"timestamp":1785730645570,"type":"function_call","name":"Write","arguments":"{\"file_path\":\"/tmp/proj/main.go\",\"content\":\"package main\\nfunc main() {}\\n\"}","providerData":{"messageId":"msg-1"},"cwd":"/tmp/proj"}`+"\n"+
			`{"timestamp":1785730645580,"type":"function_call","name":"TaskUpdate","arguments":"{}","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":100,"completion_tokens":10}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.LinesAdded != 2 || entry.LinesRemoved != 0 {
		t.Fatalf("lines = +%d/-%d, want +2/-0", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Entity != "/tmp/proj/main.go" || entry.EntityType != "file" {
		t.Fatalf("entity = %q (%q), want /tmp/proj/main.go (file)", entry.Entity, entry.EntityType)
	}
	if entry.IsWrite == nil || !*entry.IsWrite {
		t.Fatal("IsWrite not set")
	}
	if entry.Language != "Go" {
		t.Fatalf("language = %q, want Go", entry.Language)
	}
}

// TestEditCountsBothSides verifies an Edit call counts old_string as removed
// and new_string as added, falling back to the nearest preceding entry when
// its turn has no usage entry of its own.
func TestEditCountsBothSides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"timestamp":1785730645570,"type":"message","providerData":{"messageId":"msg-1","model":"glm-5.2","rawUsage":{"prompt_tokens":100,"completion_tokens":10}},"sessionId":"session-a","cwd":"/tmp/proj"}`+"\n"+
			`{"timestamp":1785730645580,"type":"function_call","name":"Edit","arguments":"{\"file_path\":\"src/app.py\",\"old_string\":\"a\\nb\\nc\",\"new_string\":\"a\"}","providerData":{"messageId":"msg-orphan"},"cwd":"/tmp/proj"}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.LinesAdded != 1 || entry.LinesRemoved != 3 {
		t.Fatalf("lines = +%d/-%d, want +1/-3", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Entity != "/tmp/proj/src/app.py" {
		t.Fatalf("entity = %q, want /tmp/proj/src/app.py (relative path resolved against cwd)", entry.Entity)
	}
	if entry.Language != "Python" {
		t.Fatalf("language = %q, want Python", entry.Language)
	}
}

// TestSkipsRecordsWithoutUsage verifies that transcript noise — reasoning,
// file-history snapshots, tool results without rawUsage — produces nothing.
func TestSkipsRecordsWithoutUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "proj", "session-a.jsonl")
	providertest.WriteFile(t, path,
		`{"timestamp":1785730645570,"type":"reasoning","content":"thinking"}`+"\n"+
			`{"timestamp":1785730645571,"type":"file-history-snapshot"}`+"\n"+
			`{"timestamp":1785730645572,"type":"function_call_result","providerData":{}}`+"\n"+
			`{"timestamp":1785730645573,"type":"message","providerData":{"messageId":"msg-0","rawUsage":{"prompt_tokens":0,"completion_tokens":0}}}`+"\n")

	entries, err := loadEntries([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want 0: %#v", len(entries), entries)
	}
}
