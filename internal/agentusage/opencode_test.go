package agentusage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// openCodeDB builds a database shaped like the one OpenCode writes: a session
// row, message rows carrying the token block, and part rows carrying the tool
// calls that touched files.
type openCodeRow struct {
	id        string
	sessionID string
	created   int64
	data      string
}

func writeOpenCodeDB(t *testing.T, sessions, messages, parts []openCodeRow) string {
	t.Helper()
	return writeOpenCodeDBAt(t, filepath.Join(t.TempDir(), "opencode.db"), sessions, messages, parts)
}

func writeOpenCodeDBAt(t *testing.T, path string, sessions, messages, parts []openCodeRow) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, version TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range sessions {
		if _, err := db.Exec(`INSERT INTO session (id, directory, version) VALUES (?, ?, ?)`, row.id, row.sessionID, row.data); err != nil {
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

// TestOpenCodeBillsRunningInputAndPerTurnOutput pins down the token semantics.
// OpenCode's "input" is the whole context resent every turn, so only its growth
// is this turn's cost, while "output" and "reasoning" already describe one turn
// and must be taken as reported. Getting this backwards inflated totals ninefold.
func TestOpenCodeBillsRunningInputAndPerTurnOutput(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":1000,"output":50,"reasoning":20,"total":1070}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":1400,"output":30,"reasoning":10,"total":1440}}`},
		}, nil)

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	sortEntries(entries)

	first, second := entries[0], entries[1]
	if first.Usage.InputTokens != 1000 || first.Usage.OutputTokens != 50 || first.Usage.ReasoningOutputTokens != 20 {
		t.Fatalf("first usage = %+v, want the full first turn", first.Usage)
	}
	// 1400 - 1000: the second turn only paid for the context it added.
	if second.Usage.InputTokens != 400 {
		t.Fatalf("second input = %d, want 400 (growth only)", second.Usage.InputTokens)
	}
	if second.Usage.OutputTokens != 30 || second.Usage.ReasoningOutputTokens != 10 {
		t.Fatalf("second output/reasoning = %d/%d, want 30/10 as reported",
			second.Usage.OutputTokens, second.Usage.ReasoningOutputTokens)
	}
	// The message's own "total" sums the running counters, so it must not leak
	// into the billed total.
	if second.Usage.TotalTokens != 440 {
		t.Fatalf("second total = %d, want 440 (billed fields only)", second.Usage.TotalTokens)
	}
}

// TestOpenCodeIgnoresZeroTokenMessages guards the counter against user turns,
// which carry an all-zero token block. Treating one as a counter reset made the
// next assistant turn pay for the whole context a second time.
func TestOpenCodeIgnoresZeroTokenMessages(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":1000,"output":50}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"role":"user","tokens":{"input":0,"output":0,"cache":{"read":0,"write":0}}}`},
			{id: "msg-3", sessionID: "ses-1", created: 3000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":1200,"output":20}}`},
		}, nil)

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (the user turn bills nothing)", len(entries))
	}
	sortEntries(entries)
	if got := entries[1].Usage.InputTokens; got != 200 {
		t.Fatalf("input after a user turn = %d, want 200 (the user turn must not reset the counter)", got)
	}
}

// TestOpenCodeRestartsBillingAfterCompaction covers a shrinking counter: the
// context was compacted, so the smaller value is a fresh baseline, not a refund.
func TestOpenCodeRestartsBillingAfterCompaction(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":9000,"output":10}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":300,"output":10}}`},
		}, nil)

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	sortEntries(entries)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if got := entries[1].Usage.InputTokens; got != 300 {
		t.Fatalf("input after compaction = %d, want 300 (the whole new context)", got)
	}
}

// TestOpenCodeProjectComesFromMessageCwd pins the project to the directory the
// agent actually ran in, falling back to the session's directory.
func TestOpenCodeProjectComesFromMessageCwd(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/session-dir"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","path":{"cwd":"/repo/message-dir"},"tokens":{"input":10,"output":5}}`},
			{id: "msg-2", sessionID: "ses-1", created: 2000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":20,"output":5}}`},
			{id: "msg-3", sessionID: "ses-2", created: 3000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":30,"output":5}}`},
		}, nil)

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	sortEntries(entries)
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Project != "message-dir" || entries[0].ProjectPath != "/repo/message-dir" {
		t.Fatalf("message cwd project = %q (%q), want message-dir", entries[0].Project, entries[0].ProjectPath)
	}
	if entries[1].Project != "session-dir" {
		t.Fatalf("session fallback project = %q, want session-dir", entries[1].Project)
	}
	// A session row that does not exist leaves nothing to name the project with.
	if entries[2].Project != usage.UnknownProject {
		t.Fatalf("unknown session project = %q, want %q", entries[2].Project, usage.UnknownProject)
	}
}

// TestOpenCodeCollectsFileChangesFromTools covers all three editing tools in one
// turn, including the per-file breakdown and which file becomes the entity.
func TestOpenCodeCollectsFileChangesFromTools(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","path":{"cwd":"/repo/demo"},"tokens":{"input":10,"output":5}}`},
		},
		[]openCodeRow{
			// edit: the recorded diff wins over the raw strings.
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"tool","tool":"edit","state":{"status":"completed","input":{"filePath":"small.go","oldString":"a","newString":"b"},"metadata":{"filediff":{"filePath":"small.go","additions":3,"deletions":1}}}}`},
			// write: a relative path resolves against the cwd, content is all additions.
			{id: "prt-2", sessionID: "msg-1", data: `{"type":"tool","tool":"write","state":{"status":"completed","input":{"filePath":"big.go","content":"1\n2\n3\n4\n5"},"metadata":{"exists":false}}}`},
			// apply_patch: the per-file summary is used verbatim.
			{id: "prt-3", sessionID: "msg-1", data: `{"type":"tool","tool":"apply_patch","state":{"status":"completed","metadata":{"files":[{"filePath":"/abs/other.go","additions":2,"deletions":2}]}}}`},
		})

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	entry := entries[0]

	if entry.LinesAdded != 3+5+2 || entry.LinesRemoved != 1+0+2 {
		t.Fatalf("totals = +%d/-%d, want +10/-3", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.IsWrite == nil || !*entry.IsWrite {
		t.Fatal("IsWrite = false, want true when files changed")
	}
	// big.go changed 5 lines, more than small.go's 4 and other.go's 4.
	if entry.Entity != "/repo/demo/big.go" {
		t.Fatalf("entity = %q, want the most-changed file", entry.Entity)
	}
	if entry.EntityType != "file" {
		t.Fatalf("entity type = %q, want file", entry.EntityType)
	}

	want := map[string]usage.FileChange{
		"/repo/demo/small.go": {Path: "/repo/demo/small.go", LinesAdded: 3, LinesRemoved: 1},
		"/repo/demo/big.go":   {Path: "/repo/demo/big.go", LinesAdded: 5},
		"/abs/other.go":       {Path: "/abs/other.go", LinesAdded: 2, LinesRemoved: 2},
	}
	if len(entry.Files) != len(want) {
		t.Fatalf("files = %+v, want %d entries", entry.Files, len(want))
	}
	for _, got := range entry.Files {
		expected, ok := want[got.Path]
		if !ok {
			t.Fatalf("unexpected file %q", got.Path)
		}
		if got != expected {
			t.Fatalf("file %q = %+v, want %+v", got.Path, got, expected)
		}
	}
}

// TestOpenCodeIgnoresSnapshotPatchParts guards against counting git snapshots as
// agent edits. A "patch" part lists whatever the worktree snapshot covered —
// build output, .DS_Store — and records no line counts at all.
func TestOpenCodeIgnoresSnapshotPatchParts(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":10,"output":5}}`},
		},
		[]openCodeRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"patch","hash":"deadbeef","files":["/repo/demo/.DS_Store"]}`},
		})

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if len(entries[0].Files) != 0 || entries[0].IsWrite != nil {
		t.Fatalf("snapshot part produced files %+v / IsWrite %v, want none",
			entries[0].Files, entries[0].IsWrite)
	}
}

// TestOpenCodeSkipsIncompleteToolCalls keeps a tool that errored or is still
// running out of the line counts.
func TestOpenCodeSkipsIncompleteToolCalls(t *testing.T) {
	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","tokens":{"input":10,"output":5}}`},
		},
		[]openCodeRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"tool","tool":"write","state":{"status":"error","input":{"filePath":"/repo/demo/a.go","content":"x"}}}`},
			{id: "prt-2", sessionID: "msg-1", data: `{"type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"/repo/demo/b.go"}}}`},
		})

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries[0].Files) != 0 {
		t.Fatalf("files = %+v, want none from a failed write and a read", entries[0].Files)
	}
}

// TestOpenCodeParsesPatchEnvelope covers apply_patch without a per-file summary,
// where the raw envelope is the only record of what changed. One envelope can
// carry several files, and a rename keeps the diff under the new name.
func TestOpenCodeParsesPatchEnvelope(t *testing.T) {
	patch := "*** Begin Patch\\n" +
		"*** Add File: new.go\\n" +
		"+package main\\n" +
		"+func main() {}\\n" +
		"*** Update File: old.go\\n" +
		"-was here\\n" +
		"+is here\\n" +
		"*** Move to: renamed.go\\n" +
		"*** End Patch"

	path := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{
			{id: "msg-1", sessionID: "ses-1", created: 1000, data: `{"role":"assistant","modelID":"gpt-5","path":{"cwd":"/repo/demo"},"tokens":{"input":10,"output":5}}`},
		},
		[]openCodeRow{
			{id: "prt-1", sessionID: "msg-1", data: `{"type":"tool","tool":"apply_patch","state":{"status":"completed","input":{"patchText":"` + patch + `"}}}`},
		})

	entries, err := loadOpenCodeDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	if entry.LinesAdded != 3 || entry.LinesRemoved != 1 {
		t.Fatalf("totals = +%d/-%d, want +3/-1", entry.LinesAdded, entry.LinesRemoved)
	}

	got := make(map[string]usage.FileChange, len(entry.Files))
	for _, file := range entry.Files {
		got[file.Path] = file
	}
	if change := got["/repo/demo/new.go"]; change.LinesAdded != 2 || change.LinesRemoved != 0 {
		t.Fatalf("new.go = %+v, want +2/-0", change)
	}
	// The rename carries old.go's diff over to its new name.
	if change := got["/repo/demo/renamed.go"]; change.LinesAdded != 1 || change.LinesRemoved != 1 {
		t.Fatalf("renamed.go = %+v, want +1/-1", change)
	}
	if _, ok := got["/repo/demo/old.go"]; ok {
		t.Fatal("old.go still present, want it recorded under the new name")
	}
}

// TestOpenCodeEntryIDSurvivesRelocation keys entries to the message id OpenCode
// assigned, so the same message read from another database file or from the
// legacy JSON layout is recognised as one event rather than counted twice.
func TestOpenCodeEntryIDSurvivesRelocation(t *testing.T) {
	message := `{"role":"assistant","modelID":"gpt-5","path":{"cwd":"/repo/demo"},"tokens":{"input":10,"output":5}}`
	first := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/repo/demo"}},
		[]openCodeRow{{id: "msg-1", sessionID: "ses-1", created: 1000, data: message}}, nil)
	second := writeOpenCodeDB(t,
		[]openCodeRow{{id: "ses-1", sessionID: "/elsewhere/demo"}},
		[]openCodeRow{{id: "msg-1", sessionID: "ses-1", created: 1000, data: message}}, nil)

	a, err := loadOpenCodeDatabase(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadOpenCodeDatabase(second)
	if err != nil {
		t.Fatal(err)
	}
	if a[0].ID != b[0].ID {
		t.Fatalf("ids differ across databases: %q vs %q", a[0].ID, b[0].ID)
	}
	if a[0].ID != stableOpenCodeMessageID("msg-1") {
		t.Fatalf("id = %q, want the message-keyed id", a[0].ID)
	}
}

// TestOpenCodeReadsEveryChannelDatabase covers OpenCode's release channels,
// which sit side by side in the data root as separate databases.
func TestOpenCodeReadsEveryChannelDatabase(t *testing.T) {
	root := t.TempDir()
	for _, channel := range []string{"opencode.db", "opencode-nightly.db"} {
		writeOpenCodeDBAt(t, filepath.Join(root, channel),
			[]openCodeRow{{id: "ses-" + channel, sessionID: "/repo/demo"}},
			[]openCodeRow{{
				id:        "msg-" + channel,
				sessionID: "ses-" + channel,
				created:   1000,
				data:      `{"role":"assistant","modelID":"gpt-5","tokens":{"input":10,"output":5}}`,
			}}, nil)
	}

	entries, err := loadOpenCodeRoot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want one per channel database", len(entries))
	}
}
