package codex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
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
	// Usage is the turn's own last_token_usage, with input_tokens split into
	// non-cached (40-8) and cache read (8). The cumulative counter is never
	// billed: here it already holds 110 tokens of history this turn did not
	// spend.
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

func TestReadUsageFileSkipsReplaysAndBillsLastUsage(t *testing.T) {
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
	// vanish; the third is billed at its own last_token_usage.
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (duplicate skipped)", len(entries))
	}
	if entries[0].Usage.TotalTokens != 15 {
		t.Fatalf("first total = %d, want 15", entries[0].Usage.TotalTokens)
	}
	second := entries[1].Usage
	if second.InputTokens != 21 || second.CacheReadInputTokens != 4 || second.OutputTokens != 12 || second.TotalTokens != 37 {
		t.Fatalf("second usage = %+v, want last usage 21/4/12/37", second)
	}
	if entries[0].ID == entries[1].ID {
		t.Fatal("distinct events share an id")
	}
}

// A forked session starts with its parent's cumulative total, and a subagent
// thread shares its parent's counter: the counter jumps by far more than the
// turn cost. The turn is billed at what it cost, never at the jump.
func TestReadUsageFileIgnoresCounterJumpsFromForksAndSubagents(t *testing.T) {
	content := `{"timestamp":"2026-07-17T01:17:55Z","type":"session_meta","payload":{"id":"fork-1","cwd":"/repo/app","forked_from_id":"parent-1"}}
{"timestamp":"2026-07-17T01:18:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":17000,"cached_input_tokens":0,"output_tokens":342,"reasoning_output_tokens":0,"total_tokens":17342},"total_token_usage":{"input_tokens":21000000,"cached_input_tokens":0,"output_tokens":498389,"reasoning_output_tokens":0,"total_tokens":21498389}}}}
{"timestamp":"2026-07-17T01:18:30Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":14000,"cached_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":0,"total_tokens":14002},"total_token_usage":{"input_tokens":39960000,"cached_input_tokens":0,"output_tokens":512391,"reasoning_output_tokens":0,"total_tokens":40472391}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-fork.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Usage.TotalTokens != 17342 || entries[1].Usage.TotalTokens != 14002 {
		t.Fatalf("totals = %d/%d, want the turns' own 17342/14002, not the counter", entries[0].Usage.TotalTokens, entries[1].Usage.TotalTokens)
	}
}

// A token_count whose turn cost nothing is not a request.
func TestReadUsageFileSkipsZeroUsageTurns(t *testing.T) {
	content := `{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app"}}
{"timestamp":"2026-06-04T01:02:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":0},"total_token_usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":0}}}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-zero.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want none", len(entries))
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

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := withoutTools(all)
	assertToolCall(t, all, "c1", "apply_patch", nil, usage.ToolStatusOK)
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

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := withoutTools(all)
	assertToolCall(t, all, "c1", "apply_patch", nil, usage.ToolStatusError)
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

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := withoutTools(all)
	assertToolCall(t, all, "c1", "exec_command", []string{"apply_patch"}, usage.ToolStatusOK)
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

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := withoutTools(all)
	assertToolCall(t, all, "c1", "apply_patch", nil, usage.ToolStatusOK)
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

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := withoutTools(all)
	assertToolCall(t, all, "c1", "apply_patch", nil, usage.ToolStatusError)
	if len(entries) != 1 || entries[0].IsWrite != nil {
		t.Fatalf("failed patch counted: %+v", entries[0])
	}
}

func withoutTools(entries []usage.Entry) []usage.Entry {
	var kept []usage.Entry
	for _, entry := range entries {
		if entry.Tool == nil {
			kept = append(kept, entry)
		}
	}
	return kept
}

// assertToolCall checks that entries hold call id's invocation half, naming
// tool and programs, and its outcome half with status ("" for none).
func assertToolCall(t *testing.T, entries []usage.Entry, id, tool string, programs []string, status string) {
	t.Helper()
	var call, result *usage.Entry
	for i := range entries {
		entry := &entries[i]
		if entry.Tool == nil || entry.Tool.CallID != id {
			continue
		}
		switch entry.EventKind {
		case usage.EventKindToolCall:
			call = entry
		case usage.EventKindToolResult:
			result = entry
		}
	}
	if call == nil || call.Tool.Name != tool || !slices.Equal(call.Tool.Programs, programs) ||
		call.ID != usage.StableID("codex", usage.EventKindToolCall, id) || call.Usage != (usage.TokenUsage{}) {
		t.Fatalf("call %s = %+v, want %s %q", id, call, tool, programs)
	}
	switch {
	case status == "" && result != nil:
		t.Fatalf("call %s has outcome %+v, want none", id, result.Tool)
	case status != "" && (result == nil || result.Tool.Status != status || result.Tool.Name != ""):
		t.Fatalf("call %s outcome = %+v, want %s", id, result, status)
	}
}

// Every shape codex has recorded a tool call in, and every place it records an
// outcome. One call is one call however many lines speak of it.
func TestReadUsageFileEmitsToolCallsInEveryShape(t *testing.T) {
	content := `{"timestamp":"2026-10-06T01:00:00Z","type":"session_meta","payload":{"id":"session-1","cwd":"/repo/app","originator":"codex_vscode"}}
{"timestamp":"2026-10-06T01:00:00Z","type":"turn_context","payload":{"cwd":"/repo/app","model":"gpt-5.5"}}
{"timestamp":"2026-10-06T01:00:01Z","type":"response_item","payload":{"type":"function_call","call_id":"c-flat","name":"mcp__supabase__execute_sql","arguments":"{\"query\":\"select 1\"}"}}
{"timestamp":"2026-10-06T01:00:02Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c-flat","output":"Wall time: 0.1 seconds\nOutput:\nExit code: 1"}}
{"timestamp":"2026-10-06T01:00:03Z","type":"response_item","payload":{"type":"function_call","call_id":"c-ns","name":"click","namespace":"mcp__computer_use","arguments":"{}"}}
{"timestamp":"2026-10-06T01:00:04Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"c-ns","server":"computer-use","tool":"click","status":"failed"}}}
{"timestamp":"2026-10-06T01:00:05Z","type":"response_item","payload":{"type":"function_call","call_id":"c-under","name":"js","namespace":"mcp__node_repl__","arguments":"{}"}}
{"timestamp":"2026-10-06T01:00:06Z","type":"response_item","payload":{"type":"function_call","call_id":"c-web","name":"run","namespace":"web","arguments":"{}"}}
{"timestamp":"2026-10-06T01:00:07Z","type":"response_item","payload":{"type":"function_call","call_id":"c-exec","name":"exec_command","arguments":"{\"cmd\":\"rg -n \\\"a|b\\\" src | sed -n 1p\"}"}}
{"timestamp":"2026-10-06T01:00:08Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c-exec","output":"Chunk ID: ab12\nWall time: 0.1 seconds\nProcess exited with code 1\nOriginal token count: 3\nOutput:\nExit code: 0\n"}}
{"timestamp":"2026-10-06T01:00:09Z","type":"response_item","payload":{"type":"function_call","call_id":"c-long","name":"exec_command","arguments":"{\"cmd\":\"pnpm dev\"}"}}
{"timestamp":"2026-10-06T01:00:10Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c-long","output":"Chunk ID: cd34\nWall time: 10.0 seconds\nProcess running with session ID 4242\nOutput:\nready"}}
{"timestamp":"2026-10-06T01:00:11Z","type":"response_item","payload":{"type":"function_call","call_id":"c-argv","name":"shell","arguments":"{\"command\":[\"zsh\",\"-lc\",\"git status\"]}"}}
{"timestamp":"2026-10-06T01:00:12Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c-argv","output":"{\"output\":\"clean\",\"metadata\":{\"exit_code\":0,\"duration_seconds\":0.1}}"}}
{"timestamp":"2026-10-06T01:00:13Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c-wrap","name":"exec","input":"text(await tools.exec_command({cmd:'nl -ba f'}))"}}
{"timestamp":"2026-10-06T01:00:20Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/zsh","-lc","nl -ba f | sed -n 1p"],"status":"failed","exit_code":2,"duration":{"secs":2,"nanos":500000000}}}}
{"timestamp":"2026-10-06T01:00:21Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"exec-2","server":"node_repl","tool":"js","status":"completed"}}}
{"timestamp":"2026-10-06T01:00:22Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c-wrap","output":[{"type":"input_text","text":"Script completed\nWall time 9.0 seconds"}]}}
{"timestamp":"2026-10-06T01:00:23Z","type":"response_item","payload":{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"x"}}}
{"timestamp":"2026-10-06T01:00:24Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"WebSearch","id":"ws_1","query":"x"}}}
{"timestamp":"2026-10-06T01:00:25Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"SubAgentActivity","id":"c-web","kind":"started"}}}
{"timestamp":"2026-10-06T01:00:26Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"item-9"}}}
{"timestamp":"2026-10-06T01:00:27Z","type":"response_item","payload":{"type":"function_call_output","call_id":"never-called","output":"Exit code: 0"}}
`
	path := filepath.Join(t.TempDir(), "sessions", "rollout-x.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, content)

	all, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}

	assertToolCall(t, all, "c-flat", "execute_sql", nil, "") // an MCP tool's own text is not an outcome
	assertToolCall(t, all, "c-ns", "click", nil, usage.ToolStatusError)
	assertToolCall(t, all, "c-under", "js", nil, "")
	assertToolCall(t, all, "c-web", "web.run", nil, "")
	assertToolCall(t, all, "c-exec", "exec_command", []string{"rg", "sed"}, usage.ToolStatusError)
	assertToolCall(t, all, "c-long", "exec_command", []string{"pnpm"}, "")
	assertToolCall(t, all, "c-argv", "shell", []string{"git"}, usage.ToolStatusOK)
	assertToolCall(t, all, "c-wrap", "exec", nil, "")
	assertToolCall(t, all, "exec-1", "exec_command", []string{"nl", "sed"}, usage.ToolStatusError)
	assertToolCall(t, all, "exec-2", "js", nil, usage.ToolStatusOK)
	assertToolCall(t, all, "ws_1", "web_search", nil, "")

	servers := map[string]string{}
	var calls, results int
	for _, entry := range all {
		switch entry.EventKind {
		case usage.EventKindToolCall:
			calls++
			servers[entry.Tool.CallID] = entry.Tool.MCPServer
			if entry.SessionID != "session-1" || entry.Project != "app" || entry.Model != "gpt-5.5" || entry.Client != "codex_vscode" {
				t.Fatalf("call context = %+v", entry)
			}
			if entry.Tool.CallID == "exec-1" && !entry.Timestamp.Equal(time.Date(2026, 10, 6, 1, 0, 17, 500000000, time.UTC)) {
				t.Fatalf("code-mode call started at %v, want its finish minus its duration", entry.Timestamp)
			}
		case usage.EventKindToolResult:
			results++
			if entry.Model != "" {
				t.Fatalf("outcome carries a model: %+v", entry)
			}
		}
	}
	if calls != 11 || results != 5 {
		t.Fatalf("calls=%d results=%d, want 11/5 — one per call, however many lines name it", calls, results)
	}
	want := map[string]string{"c-flat": "supabase", "c-ns": "computer_use", "c-under": "node_repl", "exec-2": "node_repl", "c-web": "", "c-exec": ""}
	for id, server := range want {
		if servers[id] != server {
			t.Fatalf("server of %s = %q, want %q", id, servers[id], server)
		}
	}
}
