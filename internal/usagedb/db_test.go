package usagedb

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
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
	if len(pending) != 1 || pending[0].ID != "event-old" {
		t.Fatalf("pending = %+v, want oldest event first", pending)
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
