package usagedb

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
	bolt "go.etcd.io/bbolt"
)

func TestInsertEventsIgnoresDuplicateIDs(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "usage.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	entry := usage.Entry{
		ID:         "event-a",
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

	summaries, err := db.DailyProjectSummaries("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].TotalTokens != 3 {
		t.Fatalf("total tokens = %d, want 3", summaries[0].TotalTokens)
	}

	events, err := db.UsageEvents()
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Language != usage.UnknownLanguage {
		t.Fatalf("language = %q, want Unknown", events[0].Language)
	}
}

func TestUploadStateTracksPendingUploadedFailedAndRejected(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "usage.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	entry := testUsageEntry("event-a")
	inserted, err := db.InsertEvents([]usage.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 {
		t.Fatalf("inserted = %d, want 1", inserted)
	}

	pending, err := db.PendingUsageEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-a" {
		t.Fatalf("pending = %+v, want event-a", pending)
	}

	if err := db.MarkEventsUploadFailed([]string{"event-a"}, "offline"); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingUsageEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "event-a" {
		t.Fatalf("pending after failure = %+v, want event-a", pending)
	}

	if err := db.MarkEventsUploaded([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingUsageEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after upload = %d, want 0", len(pending))
	}

	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-b")}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsRejected(map[string]string{"event-b": "bad payload"}); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingUsageEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after rejection = %d, want 0", len(pending))
	}
}

func TestPendingUsageEventsTreatsMissingUploadStateAsPending(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "usage.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	entry := testUsageEntry("legacy-event")
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(usageEventsBucket).Put([]byte(entry.ID), data)
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := db.PendingUsageEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "legacy-event" {
		t.Fatalf("pending = %+v, want legacy-event", pending)
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
