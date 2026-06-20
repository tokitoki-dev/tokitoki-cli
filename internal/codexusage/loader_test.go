package codexusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadUsageFileParsesTokenCountEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "2026", "06", "03", "rollout-session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-06-03T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tokitoki"}}
{"timestamp":"2026-06-03T01:02:04Z","type":"turn_context","payload":{"cwd":"/Users/me/workspace/tokitoki","model":"gpt-5.2-codex"}}
{"timestamp":"2026-06-03T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110},"last_token_usage":{"input_tokens":40,"cached_input_tokens":8,"output_tokens":5,"reasoning_output_tokens":2,"total_tokens":45}}}}
{"timestamp":"2026-06-03T01:02:06Z","type":"event_msg","payload":{"type":"agent_message","message":"ignored"}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.Project != "tokitoki" {
		t.Fatalf("project = %q, want tokitoki", entry.Project)
	}
	if entry.ProjectPath != "/Users/me/workspace/tokitoki" {
		t.Fatalf("project path = %q, want cwd", entry.ProjectPath)
	}
	if entry.SessionID != "session-a" {
		t.Fatalf("session id = %q, want session-a", entry.SessionID)
	}
	if entry.Model != "gpt-5.2-codex" {
		t.Fatalf("model = %q, want gpt-5.2-codex", entry.Model)
	}
	if entry.Language != "Unknown" {
		t.Fatalf("language = %q, want Unknown", entry.Language)
	}
	// input_tokens (40) is the full prompt incl. cache; we report non-cached
	// input (40 - 8 = 32) and move the cached portion to cache read, matching
	// ccusage's codex token accounting.
	if entry.Usage.InputTokens != 32 {
		t.Fatalf("input tokens = %d, want non-cached input (40-8)", entry.Usage.InputTokens)
	}
	if entry.Usage.CacheReadInputTokens != 8 {
		t.Fatalf("cache read tokens = %d, want 8 (cached portion)", entry.Usage.CacheReadInputTokens)
	}
	if entry.Usage.ReasoningOutputTokens != 2 {
		t.Fatalf("reasoning output tokens = %d, want 2", entry.Usage.ReasoningOutputTokens)
	}
	if entry.Usage.TotalTokens != 45 {
		t.Fatalf("total tokens = %d, want 45", entry.Usage.TotalTokens)
	}
}

func TestReadUsageFileInfersLanguageFromPriorToolPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "2026", "06", "03", "rollout-session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-06-03T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tokitoki"}}
{"timestamp":"2026-06-03T01:02:04Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"sed -n '1,20p' internal/httpapi/server.go\",\"workdir\":\"/Users/me/workspace/tokitoki\"}"}}
{"timestamp":"2026-06-03T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}
{"timestamp":"2026-06-03T01:02:06Z","type":"event_msg","payload":{"type":"patch_apply_end","changes":{"/Users/me/workspace/app/page.tsx":{"status":"modified"}}}}
{"timestamp":"2026-06-03T01:02:07Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23}}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Language != "Go" {
		t.Fatalf("first language = %q, want Go", entries[0].Language)
	}
	if entries[1].Language != "TypeScript" {
		t.Fatalf("second language = %q, want TypeScript", entries[1].Language)
	}
}

func TestUsageFilesIncludesSessionsAndArchivedSessions(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "sessions", "2026", "06", "03", "active.jsonl")
	archived := filepath.Join(dir, "archived_sessions", "archived.jsonl")
	mkdirAll(t, filepath.Dir(active))
	mkdirAll(t, filepath.Dir(archived))
	writeFile(t, active, "{}")
	writeFile(t, archived, "{}")

	files := UsageFiles([]string{dir})

	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
}

func TestLoadEntriesFiltersByProjectOrProjectPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "2026", "06", "03", "rollout-session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-06-03T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tokitoki"}}
{"timestamp":"2026-06-03T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}}
`)

	entries, err := LoadEntriesFromPaths([]string{dir}, "tokitoki")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	entries, err = LoadEntriesFromPaths([]string{dir}, "/Users/me/workspace/tokitoki")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries by path) = %d, want 1", len(entries))
	}

	entries, err = LoadEntriesFromPaths([]string{dir}, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries for other) = %d, want 0", len(entries))
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
