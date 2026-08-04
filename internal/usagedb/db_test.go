package usagedb

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/codex"
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

func TestOpenRekeysCodexEventsToSemanticIDs(t *testing.T) {
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
	if got[0].id != codex.StableEntryID(live) {
		t.Fatalf("id = %q, want basename-keyed id %q", got[0].id, codex.StableEntryID(live))
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

func TestOpenStampsFreshDatabaseAtCurrentVersion(t *testing.T) {
	db := openTestDB(t)

	var version int
	if err := db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != eventSchemaVersion {
		t.Fatalf("user_version = %d, want %d", version, eventSchemaVersion)
	}
}

// A database written before resume offsets existed must gain the column with
// every row defaulting to 0, which means "parse from the beginning" — safe,
// just not yet incremental.
func TestOpenAddsScannedFilesOffsetToLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE usage_events (
			id TEXT PRIMARY KEY, ts INTEGER NOT NULL, payload TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending', attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL DEFAULT 0, uploaded_at INTEGER,
			last_error TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE scanned_files (
			path TEXT PRIMARY KEY, size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL
		);
		INSERT INTO scanned_files (path, size, mtime_ns) VALUES ('/tmp/a.jsonl', 42, 7);
		PRAGMA user_version = 2;
	`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	states, err := db.ScannedFiles()
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states["/tmp/a.jsonl"]
	if !ok {
		t.Fatal("legacy scanned_files row was lost")
	}
	if state.Size != 42 || state.MtimeNS != 7 {
		t.Fatalf("legacy row = %+v, want size 42 mtime 7", state)
	}
	if state.Offset != 0 {
		t.Fatalf("offset = %d, want 0", state.Offset)
	}

	var version int
	if err := db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != eventSchemaVersion {
		t.Fatalf("user_version = %d, want %d", version, eventSchemaVersion)
	}
}

// Two uploaders running at once must take different batches, not the same one.
func TestClaimEventsGivesEachCallerADistinctBatch(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{
		testUsageEntry("event-a"), testUsageEntry("event-b"),
	}); err != nil {
		t.Fatal(err)
	}

	first, err := db.ClaimEvents(time.Now(), 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.ClaimEvents(time.Now(), 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("claims = %d and %d, want 1 each", len(first), len(second))
	}
	if first[0].ID == second[0].ID {
		t.Fatalf("both callers claimed %q; claims must not overlap", first[0].ID)
	}

	// Everything is claimed, and no lease has expired, so there is nothing left.
	third, err := db.ClaimEvents(time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 0 {
		t.Fatalf("third claim = %d events, want 0 while leases are live", len(third))
	}
}

// A batch left claimed by a process that died must become claimable again once
// its lease expires — otherwise those events would never be sent.
func TestClaimEventsReclaimsAfterLeaseExpires(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a")}); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimEvents(time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("first claim = %d, want 1", len(claimed))
	}

	// Before the lease expires the batch stays with its owner.
	if again, err := db.ClaimEvents(time.Now(), 10, time.Minute); err != nil {
		t.Fatal(err)
	} else if len(again) != 0 {
		t.Fatalf("claimed %d events while the lease was live, want 0", len(again))
	}

	// After it expires the batch is reclaimable.
	reclaimed, err := db.ClaimEvents(time.Now().Add(2*time.Minute), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != "event-a" {
		t.Fatalf("reclaimed = %+v, want event-a", reclaimed)
	}
}

// Expired leases are reclaimed alongside fresh work rather than only when the
// queue runs dry. A machine whose queue never empties would otherwise never
// reach the recovery path, stranding those events indefinitely.
func TestClaimEventsReclaimsExpiredLeasesAlongsideFreshWork(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-stuck")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimEvents(time.Now(), 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	// event-stuck is now claimed with a lease that has long expired.
	later := time.Now().Add(2 * time.Minute)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-fresh")}); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimEvents(later, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(claimed))
	for _, entry := range claimed {
		got[entry.ID] = true
	}
	if !got["event-fresh"] || !got["event-stuck"] {
		t.Fatalf("claimed = %v, want both event-fresh and the stranded event-stuck", got)
	}
}

// A batch that fills the limit with fresh work leaves expired leases for the
// next pass rather than exceeding what the caller asked for.
func TestClaimEventsRespectsLimitWhileReclaiming(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-stuck")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimEvents(time.Now(), 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Minute)
	if _, err := db.InsertEvents([]usage.Entry{
		testUsageEntry("event-a"), testUsageEntry("event-b"),
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimEvents(later, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d events, want exactly the 2 requested", len(claimed))
	}
}

// A failed upload returns its batch to the queue rather than leaving it
// claimed until the lease runs out.
func TestMarkEventsUploadFailedReleasesTheClaim(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimEvents(time.Now(), 10, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsUploadFailed([]string{"event-a"}, "offline"); err != nil {
		t.Fatal(err)
	}

	// Due again after backoff, without waiting out the hour-long lease.
	due := time.Now().Add(backoffBaseSeconds*time.Second + time.Second)
	claimed, err := db.ClaimEvents(due, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "event-a" {
		t.Fatalf("claimed = %+v, want event-a back in the queue", claimed)
	}
}

// Events the server never accounted for must return to the queue immediately,
// not stay claimed until their lease runs out.
func TestReleaseClaimsReturnsEventsToTheQueue(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimEvents(time.Now(), 10, time.Hour); err != nil {
		t.Fatal(err)
	}

	// While claimed it is invisible to the queue.
	if n, err := db.PendingCount(time.Now()); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Fatalf("pending count = %d while claimed, want 0", n)
	}

	if err := db.ReleaseClaims([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}

	if n, err := db.PendingCount(time.Now()); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Fatalf("pending count = %d after release, want 1", n)
	}
	claimed, err := db.ClaimEvents(time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "event-a" {
		t.Fatalf("claimed = %+v after release, want event-a", claimed)
	}
}

// Releasing must not resurrect events that already resolved.
func TestReleaseClaimsLeavesResolvedEventsAlone(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertEvents([]usage.Entry{testUsageEntry("event-a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimEvents(time.Now(), 10, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkEventsUploaded([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseClaims([]string{"event-a"}); err != nil {
		t.Fatal(err)
	}

	if n, err := db.PendingCount(time.Now()); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Fatalf("pending count = %d, want 0: an uploaded event was resurrected", n)
	}
}
