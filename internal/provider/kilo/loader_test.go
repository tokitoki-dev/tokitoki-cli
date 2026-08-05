package kilo

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// writeKiloDB builds a database shaped like the one Kilo's engine writes: a
// session row, message rows carrying the token block, and part rows carrying
// the tool calls that touched files.
type kiloRow struct {
	id        string
	sessionID string
	created   int64
	data      string
}

func writeKiloDB(t *testing.T, sessions, messages, parts []kiloRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kilo.db")
	db := providertest.OpenTestSQLite(t, path)
	defer db.Close()

	schema := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, model TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range sessions {
		if _, err := db.Exec(`INSERT INTO session (id, directory, model) VALUES (?, ?, ?)`, row.id, row.sessionID, row.data); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range messages {
		if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
			row.id, row.sessionID, row.created, row.data); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range parts {
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
			row.id, row.sessionID, "ses-1", int64(i), row.data); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func loadKilo(t *testing.T, path string) []usage.Entry {
	t.Helper()
	entries, err := Provider{}.WithPaths([]string{filepath.Dir(path)}).Entries()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestKiloBillsEachMessageAsReported pins down the token semantics that set
// Kilo apart from the OpenCode it forked: every message reports what its one
// API request consumed, so messages sum as-is — no diffing against the
// previous turn. Kilo's own session rows sum their messages the same way.
func TestKiloBillsEachMessageAsReported(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":20890,"output":102,"reasoning":39,"cache":{"read":0,"write":0}},"time":{"created":1785546752112}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"id":"msg-2","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":2508,"output":212,"reasoning":46,"cache":{"read":20864,"write":0}},"time":{"created":1785546760000}}`},
		},
		nil,
	)

	entries := loadKilo(t, path)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	var total usage.TokenUsage
	for _, entry := range entries {
		total.InputTokens += entry.Usage.InputTokens
		total.OutputTokens += entry.Usage.OutputTokens
		total.ReasoningOutputTokens += entry.Usage.ReasoningOutputTokens
		total.CacheReadInputTokens += entry.Usage.CacheReadInputTokens
	}
	want := usage.TokenUsage{
		InputTokens:           20890 + 2508,
		OutputTokens:          102 + 212,
		ReasoningOutputTokens: 39 + 46,
		CacheReadInputTokens:  0 + 20864,
	}
	if total != want {
		t.Fatalf("summed usage = %#v, want %#v", total, want)
	}
}

// TestKiloSkipsMessagesWithoutTokens covers user messages, which carry no
// token block at all: they are turns someone typed, not API requests.
func TestKiloSkipsMessagesWithoutTokens(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"user","time":{"created":1785546751000}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"id":"msg-2","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
		},
		nil,
	)

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Usage.InputTokens != 100 {
		t.Fatalf("input tokens = %d, want 100", entries[0].Usage.InputTokens)
	}
}

// TestKiloProjectComesFromMessageCwd: the project must be the directory the
// agent ran in, never a hardcoded name.
func TestKiloProjectComesFromMessageCwd(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/fallback/dir"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","path":{"cwd":"/Users/dev/workspace/gemini-editing"},"tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"id":"msg-2","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546753000}}`},
		},
		nil,
	)

	entries := loadKilo(t, path)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	projects := map[string]bool{}
	for _, entry := range entries {
		projects[entry.Project] = true
	}
	// msg-1 names its cwd; msg-2 falls back to the session directory.
	if !projects["gemini-editing"] || !projects["dir"] {
		t.Fatalf("projects = %v, want gemini-editing and dir", projects)
	}
}

// TestKiloCountsFileChangesFromFilediff: Kilo attaches the unified diff of
// every edit and write to the part, and the counts must come from it —
// including the removed side of an overwrite, which the tool input alone
// cannot see.
func TestKiloCountsFileChangesFromFilediff(t *testing.T) {
	patch := "Index: /repo/demo/main.go\\n===================================================================\\n--- /repo/demo/main.go\\n+++ /repo/demo/main.go\\n@@ -1,2 +1,3 @@\\n-old line\\n+new line one\\n+new line two\\n+new line three\\n-another old\\n"
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","path":{"cwd":"/repo/demo"},"tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
		},
		[]kiloRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"tool","tool":"write","state":{"status":"completed","input":{"filePath":"/repo/demo/main.go","content":"ignored when the diff is present"},"metadata":{"filepath":"/repo/demo/main.go","filediff":{"file":"/repo/demo/main.go","patch":"` + patch + `"}}}}`},
		},
	)

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.LinesAdded != 3 || entry.LinesRemoved != 2 {
		t.Fatalf("lines = +%d/-%d, want +3/-2", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Language != "Go" {
		t.Fatalf("language = %q, want Go", entry.Language)
	}
	if entry.Entity != "/repo/demo/main.go" {
		t.Fatalf("entity = %q, want /repo/demo/main.go", entry.Entity)
	}
}

// TestKiloFallsBackToToolInputWithoutDiff: a part without the diff still
// bounds the change through the tool's own input.
func TestKiloFallsBackToToolInputWithoutDiff(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","path":{"cwd":"/repo/demo"},"tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
		},
		[]kiloRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"tool","tool":"write","state":{"status":"completed","input":{"filePath":"main.py","content":"line one\nline two"}}}`},
		},
	)

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].LinesAdded != 2 || entries[0].LinesRemoved != 0 {
		t.Fatalf("lines = +%d/-%d, want +2/-0", entries[0].LinesAdded, entries[0].LinesRemoved)
	}
	if entries[0].Language != "Python" {
		t.Fatalf("language = %q, want Python", entries[0].Language)
	}
}

// TestKiloIgnoresSnapshotPatchParts: a "patch" part is a git snapshot of the
// whole worktree, not agent work.
func TestKiloIgnoresSnapshotPatchParts(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
		},
		[]kiloRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"patch","hash":"abc","files":["/repo/demo/.DS_Store"]}`},
			{id: "prt-2", sessionID: "msg-1", data: `{"type":"tool","tool":"write","state":{"status":"pending","input":{"filePath":"a.go","content":"x"}}}`},
		},
	)

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].LinesAdded != 0 || entries[0].LinesRemoved != 0 {
		t.Fatalf("lines = +%d/-%d, want zero from snapshot and incomplete parts", entries[0].LinesAdded, entries[0].LinesRemoved)
	}
}

// TestKiloFallsBackToSessionModel: a message without a modelID keeps its
// tokens under the model the session was started with, which the session row
// stores as a JSON object.
func TestKiloFallsBackToSessionModel(t *testing.T) {
	path := writeKiloDB(t,
		[]kiloRow{{id: "ses-1", sessionID: "/repo/demo", data: `{"id":"kilo-auto/free","providerID":"kilo","variant":"default"}`}},
		[]kiloRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`},
		},
		nil,
	)

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Model != "kilo-auto/free" {
		t.Fatalf("model = %q, want kilo-auto/free", entries[0].Model)
	}
}

// TestKiloReadsSessionsWithoutModelColumn: an old database without the model
// column must still yield the session directory, not fail the whole query.
func TestKiloReadsSessionsWithoutModelColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kilo.db")
	db := providertest.OpenTestSQLite(t, path)
	schema := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session (id, directory) VALUES ('ses-1', '/repo/legacy-project')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('msg-1', 'ses-1', 1000, ?)`,
		`{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	entries := loadKilo(t, path)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Project != "legacy-project" {
		t.Fatalf("project = %q, want legacy-project", entries[0].Project)
	}
}

// TestKiloEntryIDSurvivesRelocation: the entry ID keys on the message id Kilo
// assigned, so moving the database must not create a second identity.
func TestKiloEntryIDSurvivesRelocation(t *testing.T) {
	message := kiloRow{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"id":"msg-1","role":"assistant","modelID":"kilo-auto/free","tokens":{"input":100,"output":10,"cache":{"read":0,"write":0}},"time":{"created":1785546752000}}`}
	first := loadKilo(t, writeKiloDB(t, []kiloRow{{id: "ses-1", sessionID: "/repo/demo"}}, []kiloRow{message}, nil))
	second := loadKilo(t, writeKiloDB(t, []kiloRow{{id: "ses-1", sessionID: "/repo/demo"}}, []kiloRow{message}, nil))
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("entries = %d and %d, want 1 and 1", len(first), len(second))
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("ID changed across relocation: %q vs %q", first[0].ID, second[0].ID)
	}
	if first[0].ID != usage.StableID("kilo", "msg-1") {
		t.Fatalf("ID = %q, want StableID(kilo, msg-1)", first[0].ID)
	}
}
