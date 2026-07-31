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
	// Usage comes from the cumulative counter (first event: the counter
	// itself), with input_tokens split into non-cached (100-20) and cache
	// read (20). last_token_usage only feeds the id.
	if entry.Usage.InputTokens != 80 {
		t.Fatalf("input tokens = %d, want non-cached input (100-20)", entry.Usage.InputTokens)
	}
	if entry.Usage.CacheReadInputTokens != 20 {
		t.Fatalf("cache read tokens = %d, want 20 (cached portion)", entry.Usage.CacheReadInputTokens)
	}
	if entry.Usage.ReasoningOutputTokens != 3 {
		t.Fatalf("reasoning output tokens = %d, want 3", entry.Usage.ReasoningOutputTokens)
	}
	if entry.Usage.TotalTokens != 110 {
		t.Fatalf("total tokens = %d, want 110", entry.Usage.TotalTokens)
	}
}

func TestReadUsageFileUsesCumulativeDeltas(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15},"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
{"timestamp":"2026-06-04T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15},"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":25,"cached_input_tokens":4,"output_tokens":12,"reasoning_output_tokens":1,"total_tokens":37},"total_token_usage":{"input_tokens":35,"cached_input_tokens":4,"output_tokens":17,"reasoning_output_tokens":1,"total_tokens":52}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The second event is a duplicate emission (counter unchanged) and must
	// vanish; the third is the counter delta, not its last_token_usage.
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (duplicate skipped)", len(entries))
	}
	if entries[0].Usage.TotalTokens != 15 {
		t.Fatalf("first total = %d, want 15", entries[0].Usage.TotalTokens)
	}
	second := entries[1].Usage
	if second.InputTokens != 21 || second.CacheReadInputTokens != 4 || second.OutputTokens != 12 || second.TotalTokens != 37 {
		t.Fatalf("second usage = %+v, want delta 21/4/12/37", second)
	}
	if entries[0].ID == entries[1].ID {
		t.Fatal("distinct events share an id")
	}
}

func TestReadUsageFileFallsBackToLastUsageOnCounterReset(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":150},"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":150}}}}
{"timestamp":"2026-06-04T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":8,"cached_input_tokens":0,"output_tokens":3,"reasoning_output_tokens":0,"total_tokens":11},"total_token_usage":{"input_tokens":8,"cached_input_tokens":0,"output_tokens":3,"reasoning_output_tokens":0,"total_tokens":11}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// The counter went backwards (reset): the event keeps its own
	// last_token_usage instead of a bogus delta.
	if entries[1].Usage.TotalTokens != 11 {
		t.Fatalf("post-reset total = %d, want 11", entries[1].Usage.TotalTokens)
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

	entries, err := LoadEntriesFromPaths([]string{dir}, "tokitoki", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	entries, err = LoadEntriesFromPaths([]string{dir}, "/Users/me/workspace/tokitoki", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries by path) = %d, want 1", len(entries))
	}

	entries, err = LoadEntriesFromPaths([]string{dir}, "other", nil)
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

func TestStableEntryIDIndependentOfFileLocation(t *testing.T) {
	// The id comes from the event (session + time + tokens), never from
	// storage: archiving moves the file, and a rename must not matter either.
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":5,"reasoning_output_tokens":2,"total_tokens":15}}}}
`
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "sessions", "2026", "06", "04", "rollout-x.jsonl"),
		filepath.Join(dir, "archived_sessions", "rollout-x.jsonl"),
		filepath.Join(dir, "archived_sessions", "renamed-y.jsonl"),
	}
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		mkdirAll(t, filepath.Dir(path))
		writeFile(t, path, content)
		entries, err := ReadUsageFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries(%s) = %d, want 1", path, len(entries))
		}
		ids = append(ids, entries[0].ID)
	}
	if ids[0] != ids[1] || ids[0] != ids[2] {
		t.Fatalf("id depends on file location: %v", ids)
	}
}

func TestReadUsageFileAttributesConfirmedPatches(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /repo/app/main.go\n@@\n-old line\n+new line\n+extra line\n*** Add File: /repo/app/new.go\n+package app\n*** End Patch"}}
{"timestamp":"2026-06-04T01:02:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 0\nOutput:\nSuccess. Updated the following files:\nM /repo/app/main.go\nA /repo/app/new.go\n"}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
{"timestamp":"2026-06-04T01:02:07Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":25}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	first := entries[0]
	if first.LinesAdded != 3 || first.LinesRemoved != 1 {
		t.Fatalf("lines = +%d/-%d, want +3/-1", first.LinesAdded, first.LinesRemoved)
	}
	if first.Entity != "/repo/app/main.go" || first.EntityType != "file" {
		t.Fatalf("entity = %q/%q, want most-changed main.go/file", first.Entity, first.EntityType)
	}
	if first.IsWrite == nil || !*first.IsWrite {
		t.Fatal("isWrite not set")
	}
	if len(first.Files) != 2 {
		t.Fatalf("files = %+v, want main.go and new.go", first.Files)
	}

	second := entries[1]
	if second.IsWrite != nil || len(second.Files) != 0 {
		t.Fatalf("second entry inherited patches: %+v", second)
	}
}

func TestReadUsageFileIgnoresFailedPatches(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /repo/app/main.go\n+x\n*** End Patch"}}
{"timestamp":"2026-06-04T01:02:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 1\napply_patch: context mismatch"}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].IsWrite != nil || entries[0].LinesAdded != 0 {
		t.Fatalf("failed patch was counted: %+v", entries[0])
	}
}

func TestReadUsageFileParsesHeredocPatchInShellCall(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"exec_command","arguments":"{\"command\":[\"bash\",\"-lc\",\"apply_patch <<'EOF'\\n*** Begin Patch\\n*** Delete File: /repo/app/dead.go\\n*** End Patch\\nEOF\"]}"}}
{"timestamp":"2026-06-04T01:02:05Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Exit code: 0\nSuccess. Updated the following files:\nD /repo/app/dead.go\n"}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].IsWrite == nil || len(entries[0].Files) != 1 || entries[0].Files[0].Path != "/repo/app/dead.go" {
		t.Fatalf("heredoc patch not captured: %+v", entries[0])
	}
}

func TestReadUsageFileConfirmsPatchViaPatchApplyEnd(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: src/main.go\n+x\n*** Move to: src/renamed.go\n*** End Patch"}}
{"timestamp":"2026-06-04T01:02:05Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"c1","success":true,"stdout":"Success."}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.IsWrite == nil || entry.LinesAdded != 1 {
		t.Fatalf("patch_apply_end confirmation not applied: %+v", entry)
	}
	// Relative path resolved against cwd, and Move to wins as the final path.
	if len(entry.Files) != 1 || entry.Files[0].Path != "/repo/app/src/renamed.go" {
		t.Fatalf("files = %+v, want /repo/app/src/renamed.go", entry.Files)
	}
}

func TestReadUsageFileRejectsPatchApplyEndFailure(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: src/main.go\n+x\n*** End Patch"}}
{"timestamp":"2026-06-04T01:02:05Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"c1","success":false,"stderr":"invalid patch"}}
{"timestamp":"2026-06-04T01:02:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IsWrite != nil {
		t.Fatalf("failed patch counted: %+v", entries[0])
	}
}
