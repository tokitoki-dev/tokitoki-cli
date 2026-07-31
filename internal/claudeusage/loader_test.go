package claudeusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
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

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantSessionID string
	}{
		{
			name:          "modern",
			path:          "/home/me/.claude/projects/project-a/session-a.jsonl",
			wantSessionID: "session-a",
		},
		{
			name:          "nested",
			path:          "/home/me/.claude/projects/project-a/session-a/chat.jsonl",
			wantSessionID: "session-a",
		},
		{
			name:          "subagent",
			path:          "/home/me/.claude/projects/project-a/session-a/subagents/worker.jsonl",
			wantSessionID: "session-a",
		},
		{
			name:          "encoded absolute project path",
			path:          "/home/me/.claude/projects/-Users-eren-workspace-LABX-relink/session-a.jsonl",
			wantSessionID: "session-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if sessionID := ExtractSessionID(tt.path); sessionID != tt.wantSessionID {
				t.Fatalf("sessionID = %q, want %q", sessionID, tt.wantSessionID)
			}
		})
	}
}

func TestReadUsageFilePrefersLineCWDOverEncodedDir(t *testing.T) {
	// The encoded directory name is ambiguous: decoding it yields
	// "/Users/eren/workspace/tracklm/tracklm/nextjs". The line's cwd is the
	// truth and must win.
	path := filepath.Join(t.TempDir(), "projects", "-Users-eren-workspace-tracklm-tracklm-nextjs", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","cwd":"/Users/eren/workspace/tracklm/tracklm-nextjs","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Project != "tracklm-nextjs" {
		t.Fatalf("project = %q, want tracklm-nextjs", entries[0].Project)
	}
	if entries[0].ProjectPath != "/Users/eren/workspace/tracklm/tracklm-nextjs" {
		t.Fatalf("projectPath = %q, want /Users/eren/workspace/tracklm/tracklm-nextjs", entries[0].ProjectPath)
	}
}

func TestReadUsageFileAttributesPatchesToIssuingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	// Entry msg-1 issues two edits; small.go changes 2 lines, big.go 3. The
	// entity is the most-changed file, the line counts are the sum.
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
{"type":"user","timestamp":"2026-05-21T01:02:04Z","toolUseResult":{"filePath":"/repo/app/small.go","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["+added","-removed"," context"]}]}}
{"type":"user","timestamp":"2026-05-21T01:02:05Z","toolUseResult":{"filePath":"/repo/app/big.go","structuredPatch":[{"oldStart":1,"oldLines":0,"newStart":1,"newLines":3,"lines":["+a","+b","+c"]}]}}
{"timestamp":"2026-05-21T01:02:06Z","cwd":"/repo/app","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	first := entries[0]
	if first.LinesAdded != 4 || first.LinesRemoved != 1 {
		t.Fatalf("lines = +%d/-%d, want +4/-1", first.LinesAdded, first.LinesRemoved)
	}
	if first.Entity != "/repo/app/big.go" {
		t.Fatalf("entity = %q, want most-changed file big.go", first.Entity)
	}
	if !first.IsWrite {
		t.Fatal("isWrite = false, want true")
	}

	second := entries[1]
	if second.IsWrite || second.LinesAdded != 0 || second.Entity != "" {
		t.Fatalf("second entry inherited patch data: %+v", second)
	}

	if len(first.Files) != 2 {
		t.Fatalf("files = %+v, want small.go and big.go", first.Files)
	}
	for _, file := range first.Files {
		switch file.Path {
		case "/repo/app/small.go":
			if file.LinesAdded != 1 || file.LinesRemoved != 1 {
				t.Fatalf("small.go = +%d/-%d, want +1/-1", file.LinesAdded, file.LinesRemoved)
			}
		case "/repo/app/big.go":
			if file.LinesAdded != 3 || file.LinesRemoved != 0 {
				t.Fatalf("big.go = +%d/-%d, want +3/-0", file.LinesAdded, file.LinesRemoved)
			}
		default:
			t.Fatalf("unexpected file %q", file.Path)
		}
	}

	converted := ConvertEntries(entries)
	if converted[0].Entity != "/repo/app/big.go" || converted[0].EntityType != "file" {
		t.Fatalf("converted entity = %q/%q, want big.go/file", converted[0].Entity, converted[0].EntityType)
	}
	if len(converted[0].Files) != 2 {
		t.Fatalf("converted files = %+v, want 2", converted[0].Files)
	}
	if converted[0].IsWrite == nil || !*converted[0].IsWrite {
		t.Fatal("converted isWrite not set")
	}
	if converted[0].LinesAdded != 4 || converted[0].LinesRemoved != 1 {
		t.Fatalf("converted lines = +%d/-%d, want +4/-1", converted[0].LinesAdded, converted[0].LinesRemoved)
	}
	if converted[1].IsWrite != nil || converted[1].EntityType != "" {
		t.Fatalf("converted second entry inherited patch data: %+v", converted[1])
	}
}

func TestReadUsageFileHoldsPatchesBeforeFirstEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"type":"user","timestamp":"2026-05-21T01:02:02Z","toolUseResult":{"filePath":"/repo/app/a.go","structuredPatch":[{"lines":["+x"]}]}}
{"timestamp":"2026-05-21T01:02:03Z","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].LinesAdded != 1 || entries[0].Entity != "/repo/app/a.go" || !entries[0].IsWrite {
		t.Fatalf("pending patch not attached: %+v", entries[0])
	}
}

func TestParsePatchLineIgnoresNonPatchToolResults(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"toolUseResult":"plain text mentioning structuredPatch"}`),
		[]byte(`{"toolUseResult":{"filePath":"/a.go","structuredPatch":[]}}`),
		[]byte(`{"message":{"content":"structuredPatch"}}`),
	}
	for _, line := range lines {
		if _, ok := parsePatchLine(line); ok {
			t.Fatalf("parsePatchLine(%s) ok = true, want false", line)
		}
	}
}

func TestReadUsageFileCapturesGitBranch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","gitBranch":"feature/login","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Branch != "feature/login" {
		t.Fatalf("branch = %q, want feature/login", entries[0].Branch)
	}

	converted := ConvertEntries(entries)
	if converted[0].Branch != "feature/login" {
		t.Fatalf("converted branch = %q, want feature/login", converted[0].Branch)
	}
}

func TestReadUsageFileReportsUnknownProjectWithoutCWD(t *testing.T) {
	// The encoded directory name is not decodable ("/", "-", "_" and "." all
	// become "-"), so a line without cwd has no project.
	path := filepath.Join(t.TempDir(), "projects", "-Users-eren-workspace-LABX-relink", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Project != usage.UnknownProject {
		t.Fatalf("project = %q, want %q", entries[0].Project, usage.UnknownProject)
	}
	if entries[0].ProjectPath != "" {
		t.Fatalf("projectPath = %q, want empty", entries[0].ProjectPath)
	}
}

func TestProjectFromCWDRejectsUnusableValues(t *testing.T) {
	for _, cwd := range []string{"", "   ", "relative/path", "/"} {
		if _, _, ok := usage.ProjectFromCWD(cwd); ok {
			t.Fatalf("usage.ProjectFromCWD(%q) ok = true, want false", cwd)
		}
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
{"sessionId":"session-a","timestamp":"2026-05-21T01:02:03Z","version":"1.2.3","requestId":"req-1","cwd":"/repo/project-a","message":{"id":"msg-1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"speed":"fast"}}}
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
	if entry.Language != "Unknown" {
		t.Fatalf("language = %q, want Unknown", entry.Language)
	}
	if entry.Date != "2026-05-21" {
		t.Fatalf("date = %q, want 2026-05-21", entry.Date)
	}
	if got := tokenTotal(entry.Data.Message.Usage); got != 20 {
		t.Fatalf("tokenTotal = %d, want 20", got)
	}
}

func TestReadUsageFileInfersLanguageFromToolUseFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/server/server.go"}}]}}`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Language != "Go" {
		t.Fatalf("language = %q, want Go", entries[0].Language)
	}
}

func TestReadUsageFileDoesNotInferLanguageFromCodeFenceWithoutFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, "{\"timestamp\":\"2026-05-21T01:02:03Z\",\"message\":{\"id\":\"msg-1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"content\":[{\"type\":\"text\",\"text\":\"```tsx\\nexport default function Page() {}\\n```\"}]}}\n")

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Language != "Unknown" {
		t.Fatalf("language = %q, want Unknown", entries[0].Language)
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

	entries, err := LoadEntriesFromPaths([]string{dir}, "", nil)
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

func TestApplyPatchAccumulatesSameFile(t *testing.T) {
	entry := LoadedEntry{}
	applyPatch(&entry, patchStats{file: "/repo/a.go", added: 2, removed: 1})
	applyPatch(&entry, patchStats{file: "/repo/b.go", added: 1})
	applyPatch(&entry, patchStats{file: "/repo/a.go", added: 1})

	if len(entry.Files) != 2 {
		t.Fatalf("files = %+v, want 2", entry.Files)
	}
	if entry.Files[0].Path != "/repo/a.go" || entry.Files[0].LinesAdded != 3 || entry.Files[0].LinesRemoved != 1 {
		t.Fatalf("a.go = %+v, want +3/-1", entry.Files[0])
	}
	if entry.Entity != "/repo/a.go" {
		t.Fatalf("entity = %q, want cumulative most-changed a.go", entry.Entity)
	}
	if entry.LinesAdded != 4 || entry.LinesRemoved != 1 {
		t.Fatalf("totals = +%d/-%d, want +4/-1", entry.LinesAdded, entry.LinesRemoved)
	}
}

func TestParsePatchLineCountsCreatedFileContent(t *testing.T) {
	line := []byte(`{"toolUseResult":{"type":"create","filePath":"/repo/new.go","content":"package main\n\nfunc main() {}\n","structuredPatch":[]}}`)
	stats, ok := parsePatchLine(line)
	if !ok {
		t.Fatal("parsePatchLine ok = false, want true")
	}
	if stats.file != "/repo/new.go" || stats.added != 3 || stats.removed != 0 {
		t.Fatalf("stats = %+v, want new.go +3/-0", stats)
	}

	empty := []byte(`{"toolUseResult":{"type":"create","filePath":"/repo/empty.go","content":"","structuredPatch":[]}}`)
	stats, ok = parsePatchLine(empty)
	if !ok || stats.added != 0 {
		t.Fatalf("empty create = %+v ok=%v, want +0 ok=true", stats, ok)
	}
}
