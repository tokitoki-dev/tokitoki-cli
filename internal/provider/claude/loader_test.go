package claude

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

// The most recently modified transcript comes first so a scan reaches the
// session being worked in now before it reaches history.
func TestUsageFilesOrdersMostRecentlyModifiedFirst(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "projects", "project-a", "a-old.jsonl")
	recent := filepath.Join(dir, "projects", "project-a", "b-recent.jsonl")
	mkdirAll(t, filepath.Dir(old))
	writeFile(t, old, "{}")
	writeFile(t, recent, "{}")
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(recent, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	files := UsageFiles([]string{dir}, "")
	if len(files) != 2 || files[0] != recent {
		t.Fatalf("files = %v, want the recent transcript first", files)
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

// Every tool result carrying a diff is an event of its own, keyed on the
// tool_use id it answers. The API call that issued the edits carries none of
// the edit data: its tokens are its whole payload.
func TestReadUsageFileEmitsOneFileEditPerToolResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
{"type":"user","timestamp":"2026-05-21T01:02:04Z","cwd":"/repo/app","gitBranch":"main","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-1","content":"ok"}]},"toolUseResult":{"filePath":"/repo/app/small.go","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["+added","-removed"," context"]}]}}
{"type":"user","timestamp":"2026-05-21T01:02:05Z","cwd":"/repo/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-2","content":"ok"}]},"toolUseResult":{"filePath":"/repo/app/big.go","structuredPatch":[{"oldStart":1,"oldLines":0,"newStart":1,"newLines":3,"lines":["+a","+b","+c"]}]}}
{"timestamp":"2026-05-21T01:02:06Z","cwd":"/repo/app","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4 (2 api calls + 2 edits)", len(entries))
	}

	call := entries[0]
	if call.Kind != usage.EventKindAPICall || call.IsWrite || call.LinesAdded != 0 || call.Entity != "" || len(call.Files) != 0 {
		t.Fatalf("api call carries edit data: %+v", call)
	}

	small := entries[1]
	if small.Kind != usage.EventKindFileEdit || small.Entity != "/repo/app/small.go" || !small.IsWrite {
		t.Fatalf("small edit = %+v", small)
	}
	if small.LinesAdded != 1 || small.LinesRemoved != 1 {
		t.Fatalf("small lines = +%d/-%d, want +1/-1", small.LinesAdded, small.LinesRemoved)
	}
	if small.ToolUseID != "toolu-1" || small.ID != usage.StableID("claude", "edit", "toolu-1") {
		t.Fatalf("small edit keyed on %q / %q, want tool_use id", small.ToolUseID, small.ID)
	}
	if small.Project != "app" || small.ProjectPath != "/repo/app" || small.Branch != "main" || small.Language != "Go" {
		t.Fatalf("edit context = project %q path %q branch %q language %q", small.Project, small.ProjectPath, small.Branch, small.Language)
	}
	if small.Data.Message.Usage != (TokenUsage{}) {
		t.Fatalf("edit carries tokens: %+v", small.Data.Message.Usage)
	}

	big := entries[2]
	if big.Entity != "/repo/app/big.go" || big.LinesAdded != 3 || big.LinesRemoved != 0 {
		t.Fatalf("big edit = %+v", big)
	}
	if len(big.Files) != 1 || big.Files[0].Path != "/repo/app/big.go" || big.Files[0].LinesAdded != 3 {
		t.Fatalf("big files = %+v", big.Files)
	}

	converted := ConvertEntries(entries)
	if converted[0].EventKind != usage.EventKindAPICall || converted[0].IsWrite != nil || converted[0].EntityType != "" {
		t.Fatalf("converted api call = %+v", converted[0])
	}
	edit := converted[1]
	if edit.EventKind != usage.EventKindFileEdit || edit.EntityType != "file" || edit.Entity != "/repo/app/small.go" {
		t.Fatalf("converted edit = %+v", edit)
	}
	if edit.IsWrite == nil || !*edit.IsWrite || edit.LinesAdded != 1 || edit.LinesRemoved != 1 || len(edit.Files) != 1 {
		t.Fatalf("converted edit lines = %+v", edit)
	}
	if edit.Usage.TotalTokens != 0 || edit.Model != "" {
		t.Fatalf("converted edit carries tokens or model: %+v", edit)
	}
	if edit.Raw["tool_use_id"] != "toolu-1" {
		t.Fatalf("converted edit raw = %+v, want tool_use_id", edit.Raw)
	}
}

// Claude Code writes one API response as several lines — one per content
// block — sharing message.id, requestId and usage. Each line yields the same
// event, so the store keeps one, and an edit that follows the last block is
// an event of its own rather than data that has to find its way onto one of
// the copies.
func TestReadUsageFileSplitMessageYieldsOneIDAndKeepsItsEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","requestId":"req-1","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":323},"content":[{"type":"text","text":"editing"}]}}
{"timestamp":"2026-05-21T01:02:03Z","requestId":"req-1","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":323},"content":[{"type":"tool_use","id":"toolu-1","name":"Edit","input":{"file_path":"/repo/app/a.go"}}]}}
{"type":"user","timestamp":"2026-05-21T01:02:05Z","cwd":"/repo/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-1","content":"ok"}]},"toolUseResult":{"filePath":"/repo/app/a.go","structuredPatch":[{"lines":["+a","+b","+c","-d"]}]}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (two copies + one edit)", len(entries))
	}
	if entries[0].ID != entries[1].ID {
		t.Fatalf("copies of one message got different ids: %q vs %q", entries[0].ID, entries[1].ID)
	}
	if entries[2].Kind != usage.EventKindFileEdit || entries[2].LinesAdded != 3 || entries[2].LinesRemoved != 1 {
		t.Fatalf("edit after split message = %+v", entries[2])
	}

	deduped, err := LoadEntriesFromPaths([]string{filepath.Dir(filepath.Dir(filepath.Dir(path)))}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 2 {
		t.Fatalf("len(deduped) = %d, want 2 (one call + one edit)", len(deduped))
	}
	if deduped[0].Data.Message.Usage.OutputTokens != 323 || deduped[1].LinesAdded != 3 {
		t.Fatalf("deduped = %+v", deduped)
	}
}

// When Claude Code forks or resumes a session it copies the history into a
// new transcript, rewriting sessionId and every uuid but keeping message.id,
// requestId and the tool_use ids. The copy must contribute nothing.
func TestLoadEntriesForkedSessionCopyAddsNoEvents(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "projects", "project-a", "aaaa-original.jsonl")
	fork := filepath.Join(dir, "projects", "project-a", "bbbb-fork.jsonl")
	mkdirAll(t, filepath.Dir(original))
	history := func(session, uuid1, uuid2 string) string {
		return `{"uuid":"` + uuid1 + `","sessionId":"` + session + `","timestamp":"2026-05-21T01:02:03Z","requestId":"req-1","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":5,"output_tokens":7}}}
{"uuid":"` + uuid2 + `","sessionId":"` + session + `","type":"user","timestamp":"2026-05-21T01:02:05Z","cwd":"/repo/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-1","content":"ok"}]},"toolUseResult":{"filePath":"/repo/app/a.go","structuredPatch":[{"lines":["+a"]}]}}
`
	}
	writeFile(t, original, history("aaaa-original", "u1", "u2"))
	writeFile(t, fork, history("bbbb-fork", "u3", "u4")+
		`{"uuid":"u5","sessionId":"bbbb-fork","timestamp":"2026-05-21T02:00:00Z","requestId":"req-2","cwd":"/repo/app","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")

	entries, err := LoadEntriesFromPaths([]string{dir}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (copied call + copied edit once, plus the fork's own call)", len(entries))
	}
	var calls, edits int
	var tokens uint64
	for _, entry := range entries {
		switch entry.Kind {
		case usage.EventKindAPICall:
			calls++
			tokens += tokenTotal(entry.Data.Message.Usage)
		case usage.EventKindFileEdit:
			edits++
		}
	}
	if calls != 2 || edits != 1 || tokens != 14 {
		t.Fatalf("calls=%d edits=%d tokens=%d, want 2/1/14", calls, edits, tokens)
	}
}

// A synthetic message (an API error echo, a local notice) carries a message
// id and a usage block of zeros. Nothing was billed; it is not a request.
func TestReadUsageFileSkipsZeroUsageLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","isApiErrorMessage":true,"message":{"id":"msg-err","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"timestamp":"2026-05-21T01:02:04Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || *entries[0].Data.Message.ID != "msg-1" {
		t.Fatalf("entries = %+v, want only the billed message", entries)
	}
}

// An edit whose tool_result names no tool_use id has no identity. It is
// dropped rather than keyed on something that changes on every rescan.
func TestReadUsageFileSkipsEditWithoutToolUseID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"type":"user","timestamp":"2026-05-21T01:02:05Z","cwd":"/repo/app","message":{"role":"user","content":"ok"},"toolUseResult":{"filePath":"/repo/app/a.go","structuredPatch":[{"lines":["+a"]}]}}
{"type":"user","timestamp":"2026-05-21T01:02:06Z","cwd":"/repo/app","toolUseResult":{"filePath":"/repo/app/b.go","structuredPatch":[{"lines":["+a"]}]}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}
}

func TestParseLineIgnoresNonEditToolResults(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","toolUseResult":"plain text mentioning structuredPatch"}`),
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-1"}]},"toolUseResult":{"filePath":"/a.go","structuredPatch":[]}}`),
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"content":"structuredPatch"}}`),
		// A Read result whose file content happens to contain "type":"create".
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-1"}]},"toolUseResult":{"type":"text","file":{"filePath":"/a.json","content":"{\"type\":\"create\"}"}}}`),
	}
	for _, line := range lines {
		if _, ok := parseLine(line, "session"); ok {
			t.Fatalf("parseLine(%s) ok = true, want false", line)
		}
	}
}

func TestParseLineCountsCreatedFileContent(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-1"}]},"toolUseResult":{"type":"create","filePath":"/repo/new.go","content":"package main\n\nfunc main() {}\n","structuredPatch":[]}}`)
	entry, ok := parseLine(line, "session")
	if !ok {
		t.Fatal("parseLine ok = false, want true")
	}
	if entry.Entity != "/repo/new.go" || entry.LinesAdded != 3 || entry.LinesRemoved != 0 {
		t.Fatalf("entry = %+v, want new.go +3/-0", entry)
	}

	empty := []byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-2"}]},"toolUseResult":{"type":"create","filePath":"/repo/empty.go","content":"","structuredPatch":[]}}`)
	entry, ok = parseLine(empty, "session")
	if !ok || entry.LinesAdded != 0 || !entry.IsWrite {
		t.Fatalf("empty create = %+v ok=%v, want +0 write ok=true", entry, ok)
	}
}

// Null fields and torn lines must never reject a usable line or crash the
// parser. A null model keeps the tokens under an unknown model; a line torn
// mid-write is simply not a record yet.
func TestParseLineToleratesNullFieldsAndTornLines(t *testing.T) {
	nullModel := []byte(`{"timestamp":"2026-05-21T01:02:03Z","sessionId":null,"requestId":null,"message":{"id":"msg-1","model":null,"usage":{"input_tokens":1,"output_tokens":1,"speed":null},"content":null}}`)
	entry, ok := parseLine(nullModel, "session")
	if !ok {
		t.Fatal("line with null fields rejected")
	}
	if entry.Model != "" || entry.Data.Message.Usage.InputTokens != 1 {
		t.Fatalf("entry = %+v, want tokens kept under empty model", entry)
	}

	for _, torn := range [][]byte{
		[]byte(`"abc:null,"usage":{}`),
		[]byte(`{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","usage":{"input_tokens":1`),
		[]byte(`{"message":{"usage":{"input_tokens":"lots"}}}`),
	} {
		if _, ok := parseLine(torn, "session"); ok {
			t.Fatalf("torn line %s produced an entry", torn)
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
	if entry.Kind != usage.EventKindAPICall {
		t.Fatalf("kind = %q, want api_call", entry.Kind)
	}
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
	if entry.ID != usage.StableID("claude", "msg-1", "req-1") {
		t.Fatalf("id = %q, want message.id + requestId key", entry.ID)
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

// A session's language carries to the turns that name no file. Prose turns —
// explanations, questions, plans — are most of an assistant's messages and
// carry the larger token counts, so judging each line alone made Unknown the
// top language by tokens on work that was plainly one language.
func TestReadUsageFileCarriesSessionLanguageToProseTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/server/server.go"}}]}}
{"timestamp":"2026-05-21T01:02:04Z","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"That handler looks fine to me."}]}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	for i, entry := range entries {
		if entry.Language != "Go" {
			t.Fatalf("entries[%d].Language = %q, want Go", i, entry.Language)
		}
	}
}

// Nothing is invented: a session that never named a file stays Unknown.
func TestReadUsageFileLeavesLanguageUnknownUntilOneIsSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"Sure, what would you like to build?"}]}}
{"timestamp":"2026-05-21T01:02:04Z","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"Happy to help."}]}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	for i, entry := range entries {
		if entry.Language != "Unknown" {
			t.Fatalf("entries[%d].Language = %q, want Unknown", i, entry.Language)
		}
	}
}

// The language a line reports must not depend on where the previous scan
// stopped. A resumed scan starting after the tool call still has to know what
// the skipped prefix established, or the same line lands as Go on a full read
// and Unknown on an incremental one, and no one can tell the rows apart.
func TestReadUsageFileFromResumesWithTheSessionLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `{"timestamp":"2026-05-21T01:02:03Z","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/server/server.go"}}]}}
{"timestamp":"2026-05-21T01:02:04Z","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"That handler looks fine to me."}]}}
`)

	full, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 2 {
		t.Fatalf("len(full) = %d, want 2", len(full))
	}

	resumed, _, err := ReadUsageFileFrom(path, full[1].SourceStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("len(resumed) = %d, want 1", len(resumed))
	}
	if resumed[0].Language != full[1].Language {
		t.Fatalf("resumed language = %q, want %q (same line, full read)",
			resumed[0].Language, full[1].Language)
	}
	if resumed[0].ID != full[1].ID {
		t.Fatalf("resumed id = %q, want %q", resumed[0].ID, full[1].ID)
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

func TestLoadEntriesKeepsFirstOfDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","requestId":"req-1","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
{"timestamp":"2026-05-21T01:02:04Z","requestId":"req-1","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}
`)

	entries, err := LoadEntriesFromPaths([]string{dir}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
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

func TestReadUsageFileParsesCacheCreationBreakdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "project-a", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, `
{"timestamp":"2026-05-21T01:02:03Z","cwd":"/repo/app","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":2,"output_tokens":348,"cache_creation_input_tokens":44994,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":11,"ephemeral_1h_input_tokens":44983}}}}
{"timestamp":"2026-05-21T01:02:04Z","cwd":"/repo/app","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":7}}}
`)

	entries, err := ReadUsageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	converted := ConvertEntries(entries)
	var withBreakdown, withoutBreakdown *usage.Entry
	for i := range converted {
		if converted[i].Usage.CacheCreationInputTokens == 44994 {
			withBreakdown = &converted[i]
		} else {
			withoutBreakdown = &converted[i]
		}
	}
	if withBreakdown == nil || withoutBreakdown == nil {
		t.Fatalf("entries not found: %+v", converted)
	}
	if withBreakdown.Usage.CacheCreation5mInputTokens != 11 ||
		withBreakdown.Usage.CacheCreation1hInputTokens != 44983 {
		t.Fatalf("breakdown = 5m:%d 1h:%d, want 5m:11 1h:44983",
			withBreakdown.Usage.CacheCreation5mInputTokens,
			withBreakdown.Usage.CacheCreation1hInputTokens)
	}
	// No breakdown object means both tiers stay zero — the flat total alone
	// travels, exactly as before this field existed.
	if withoutBreakdown.Usage.CacheCreation5mInputTokens != 0 ||
		withoutBreakdown.Usage.CacheCreation1hInputTokens != 0 ||
		withoutBreakdown.Usage.CacheCreationInputTokens != 7 {
		t.Fatalf("plain entry gained a breakdown: %+v", withoutBreakdown.Usage)
	}
}
