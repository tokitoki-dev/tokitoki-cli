package usagedb

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/codexusage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestInsertEventsIgnoresDuplicateIDs(t *testing.T) {
	db := openTestDB(t)

	entry := testUsageEntry("event-a")
	inserted, err := db.InsertEvents([]usage.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 {
		t.Fatalf("inserted = %d, want 1", inserted)
	}

	inserted, err = db.InsertEvents([]usage.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 {
		t.Fatalf("duplicate inserted = %d, want 0", inserted)
	}

	pending, err := db.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-a" {
		t.Fatalf("pending = %+v, want event-a", pending)
	}
	if pending[0].Language != usage.UnknownLanguage {
		t.Fatalf("language = %q, want Unknown", pending[0].Language)
	}
}

func TestQueueTransitionsUploadedAndRejectedAreFinal(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a"), testUsageEntry("event-b")}); err != nil {
		t.Fatal(err)
	}

	if err := db.MarkEventsUploaded([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsRejected(map[string]string{"event-b": "bad payload"}); err != nil {
		t.Fatal(err)
	}

	pending, err := db.PendingEvents(time.Now().Add(24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want empty", pending)
	}
}

func TestMarkEventsUploadFailedAppliesExponentialBackoff(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a")}); err != nil {
		t.Fatal(err)
	}

	if err := db.MarkEventsUploadFailed([]string{"event-a"}, "offline"); err != nil {
		t.Fatal(err)
	}

	// Right after the first failure the event is backing off.
	pending, err := db.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending during backoff = %+v, want empty", pending)
	}

	// After the first backoff window (30s) it is due again.
	pending, err = db.PendingEvents(time.Now().Add(backoffBaseSeconds*time.Second+time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-a" {
		t.Fatalf("pending after backoff = %+v, want event-a", pending)
	}

	// A second failure doubles the delay: not due at +31s, due at +61s.
	if err := db.MarkEventsUploadFailed([]string{"event-a"}, "still offline"); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingEvents(time.Now().Add(backoffBaseSeconds*time.Second+time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending during doubled backoff = %+v, want empty", pending)
	}
	pending, err = db.PendingEvents(time.Now().Add(2*backoffBaseSeconds*time.Second+time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending after doubled backoff = %+v, want event-a", pending)
	}

	// The delay never exceeds backoffMaxSeconds no matter how often it fails.
	for i := 0; i < 20; i++ {
		if err := db.MarkEventsUploadFailed([]string{"event-a"}, "permanently offline"); err != nil {
			t.Fatal(err)
		}
	}
	pending, err = db.PendingEvents(time.Now().Add(backoffMaxSeconds*time.Second+time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending after max backoff = %+v, want event-a", pending)
	}
}

func TestPendingEventsOrdersByTimestampAndHonorsLimit(t *testing.T) {
	db := openTestDB(t)

	older := testUsageEntry("event-old")
	older.Timestamp = older.Timestamp.Add(-time.Hour)
	newer := testUsageEntry("event-new")
	if _, err := db.InsertEvents([]usage.Entry{newer, older}); err != nil {
		t.Fatal(err)
	}

	pending, err := db.PendingEvents(time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-new" {
		t.Fatalf("pending = %+v, want newest event first", pending)
	}
}

func TestPruneUploadedKeepsRecentAndQueuedEvents(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a"), testUsageEntry("event-b")}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsUploaded([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}

	// Nothing is old enough to prune yet.
	pruned, err := db.PruneUploaded(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0", pruned)
	}

	// A cutoff in the future removes the uploaded event but not the queued one.
	pruned, err = db.PruneUploaded(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	pending, err := db.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-b" {
		t.Fatalf("pending after prune = %+v, want event-b", pending)
	}
}

func testUsageEntry(id string) usage.Entry {
	return usage.Entry{
		ID:         id,
		Provider:   usage.ProviderCodex,
		SourceFile: "/tmp/session.jsonl",
		SourceLine: 1,
		Timestamp:  time.Date(2026, 6, 4, 1, 2, 3, 0, time.UTC),
		Date:       "2026-06-04",
		Project:    "tokitoki",
		Usage: usage.TokenUsage{
			InputTokens:  1,
			OutputTokens: 2,
			TotalTokens:  3,
		},
	}
}

func TestOpenRekeysCodexEventsToBasenameIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	legacyID := func(entry usage.Entry) string {
		return usage.StableID(
			string(usage.ProviderCodex),
			entry.SourceFile,
			"1",
			entry.Timestamp.Format(time.RFC3339Nano),
			entry.Model,
			"1", "0", "2", "0", "3",
		)
	}

	// The same event ingested twice: once from the live path, once after the
	// file was archived. Full-path ids made them distinct rows.
	live := testUsageEntry("")
	live.SourceFile = "/home/me/.codex/sessions/2026/06/04/rollout-x.jsonl"
	live.ID = legacyID(live)
	archived := live
	archived.SourceFile = "/home/me/.codex/archived_sessions/rollout-x.jsonl"
	archived.ID = legacyID(archived)
	if live.ID == archived.ID {
		t.Fatal("test premise broken: legacy ids should differ across paths")
	}
	if _, err := db.InsertEvents([]usage.Entry{live, archived}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsUploaded([]string{live.ID}); err != nil {
		t.Fatal(err)
	}
	// Pretend this database was written by a pre-migration binary.
	if _, err := db.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	rows, err := reopened.db.Query(`SELECT id, status FROM usage_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ id, status string }
	got := make([]row, 0)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.status); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1 (duplicates collapsed)", len(got))
	}
	if got[0].id != codexusage.StableEntryID(live) {
		t.Fatalf("id = %q, want basename-keyed id %q", got[0].id, codexusage.StableEntryID(live))
	}
	if got[0].status != "uploaded" {
		t.Fatalf("status = %q, want uploaded (uploaded copy must win)", got[0].status)
	}

	// The rewritten payload must carry the new id so future uploads use it.
	pruned, err := reopened.PruneUploaded(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
}
