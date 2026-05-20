package claudeusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageFilesLimitsDiscoveryToProjectFilter(t *testing.T) {
	dir := t.TempDir()
	projectA := filepath.Join(dir, "projects", "project-a", "session-a")
	projectB := filepath.Join(dir, "projects", "project-b", "session-b")
	mkdirAll(t, projectA)
	mkdirAll(t, projectB)
	writeFile(t, filepath.Join(projectA, "a.jsonl"), "{}")
	writeFile(t, filepath.Join(projectB, "b.jsonl"), "{}")

	files := UsageFiles([]string{dir}, "project-a")

	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if got := files[0]; !containsPathSegment(got, "project-a") {
		t.Fatalf("file = %q, want project-a path", got)
	}
}

func TestUsageFilesFallsBackForNonSegmentProjectFilter(t *testing.T) {
	dir := t.TempDir()
	projectA := filepath.Join(dir, "projects", "project-a", "session-a")
	projectB := filepath.Join(dir, "projects", "project-b", "session-b")
	mkdirAll(t, projectA)
	mkdirAll(t, projectB)
	writeFile(t, filepath.Join(projectA, "a.jsonl"), "{}")
	writeFile(t, filepath.Join(projectB, "b.jsonl"), "{}")

	files := UsageFiles([]string{dir}, "project-a/session-a")

	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
}

func TestProjectPathSegmentRejectsUnsafeValues(t *testing.T) {
	cases := map[string]bool{
		"":                    false,
		".":                   false,
		"..":                  false,
		"project-a/session-a": false,
		`project-a\session-a`: false,
		"project-a":           true,
	}

	for value, want := range cases {
		if got := isProjectPathSegment(value); got != want {
			t.Fatalf("isProjectPathSegment(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestExtractSessionParts(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		wantSessionID   string
		wantProjectPath string
	}{
		{
			name:            "modern",
			path:            "/home/me/.claude/projects/project-a/session-a.jsonl",
			wantSessionID:   "session-a",
			wantProjectPath: "project-a",
		},
		{
			name:            "nested",
			path:            "/home/me/.claude/projects/project-a/session-a/chat.jsonl",
			wantSessionID:   "session-a",
			wantProjectPath: "project-a",
		},
		{
			name:            "subagent",
			path:            "/home/me/.claude/projects/project-a/session-a/subagents/worker.jsonl",
			wantSessionID:   "session-a",
			wantProjectPath: "project-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID, projectPath := ExtractSessionParts(tt.path)
			if sessionID != tt.wantSessionID {
				t.Fatalf("sessionID = %q, want %q", sessionID, tt.wantSessionID)
			}
			if projectPath != tt.wantProjectPath {
				t.Fatalf("projectPath = %q, want %q", projectPath, tt.wantProjectPath)
			}
		})
	}
}

func TestHasUnsupportedNullField(t *testing.T) {
	rejected := [][]byte{
		[]byte(`{"message":{"usage":{"speed":null}}}`),
		[]byte(`{"message":{"model":null,"usage":{"input_tokens":0}}}`),
		[]byte(`{"sessionId":null,"message":{"usage":{"input_tokens":0}}}`),
	}
	for _, line := range rejected {
		if !hasUnsupportedNullField(line) {
			t.Fatalf("hasUnsupportedNullField(%s) = false, want true", line)
		}
	}

	allowed := []byte(`{"message":{"content":null,"usage":{"input_tokens":0}}}`)
	if hasUnsupportedNullField(allowed) {
		t.Fatalf("hasUnsupportedNullField(%s) = true, want false", allowed)
	}
}

func TestReadUsageFileParsesUsageLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a", "chat.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"type":"user","message":{"content":"hello"}}
{"sessionId":"session-a","timestamp":"2026-05-21T01:02:03Z","version":"1.2.3","requestId":"req-1","message":{"id":"msg-1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"speed":"fast"}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.Project != "project-a" {
		t.Fatalf("project = %q, want project-a", entry.Project)
	}
	if entry.SessionID != "session-a" {
		t.Fatalf("sessionID = %q, want session-a", entry.SessionID)
	}
	if entry.Model != "claude-sonnet-4-20250514-fast" {
		t.Fatalf("model = %q, want fast suffix", entry.Model)
	}
	if entry.Date != "2026-05-21" {
		t.Fatalf("date = %q, want 2026-05-21", entry.Date)
	}
	if got := tokenTotal(entry.Data.Message.Usage); got != 20 {
		t.Fatalf("tokenTotal = %d, want 20", got)
	}
}

func TestReadUsageFileSkipsUnsupportedSpeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1,"speed":"turbo"}}}`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestLoadEntriesDeduplicatesByMessageAndRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","requestId":"req-1","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
{"timestamp":"2026-05-21T01:02:04Z","requestId":"req-1","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":10,"output_tokens":1}}}
`)

	entries, err := LoadEntriesFromPaths([]string{dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if got := entries[0].Data.Message.Usage.InputTokens; got != 10 {
		t.Fatalf("input tokens = %d, want replacement with larger usage", got)
	}
}

func TestUsageLimitResetTimeFromLine(t *testing.T) {
	isAPIError := true
	line := []byte(`{"timestamp":"2026-05-21T01:02:03Z","isApiErrorMessage":true,"message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":"Claude AI usage limit reached|1779325200"}}`)

	reset := usageLimitResetTimeFromLine(line, &isAPIError)
	if reset == nil {
		t.Fatal("reset = nil, want timestamp")
	}
	if want := time.Unix(1779325200, 0).UTC(); !reset.Equal(want) {
		t.Fatalf("reset = %s, want %s", reset, want)
	}
}

func TestClaudePathsUsesConfigDir(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "claude")
	mkdirAll(t, filepath.Join(configDir, "projects"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(configDir, "projects"))

	paths, err := ClaudePaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("len(paths) = %d, want 1", len(paths))
	}
	if paths[0] != configDir {
		t.Fatalf("path = %q, want %q", paths[0], configDir)
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

func containsPathSegment(path, segment string) bool {
	for _, part := range pathParts(path) {
		if part == segment {
			return true
		}
	}
	return false
}
