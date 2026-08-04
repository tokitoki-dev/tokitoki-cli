// Package usagedb persists usage events and their upload queue state in a
// local SQLite database. Events are written first with status "pending" and
// uploaded afterwards; failed uploads back off exponentially so an offline
// machine retries calmly instead of hammering the network on every heartbeat.
package usagedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/codex"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	_ "modernc.org/sqlite"
)

const (
	// backoffBaseSeconds is the delay after the first failed upload attempt.
	// Each further failure doubles it, capped at backoffMaxSeconds.
	backoffBaseSeconds = 30
	backoffMaxSeconds  = 3600
)

const schema = `
CREATE TABLE IF NOT EXISTS usage_events (
	id              TEXT PRIMARY KEY,
	ts              INTEGER NOT NULL,
	payload         TEXT NOT NULL,
	status          TEXT NOT NULL DEFAULT 'pending',
	attempt_count   INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER NOT NULL DEFAULT 0,
	uploaded_at     INTEGER,
	last_error      TEXT NOT NULL DEFAULT '',
	lease_until     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_usage_events_queue ON usage_events(status, next_attempt_at);
CREATE TABLE IF NOT EXISTS scanned_files (
	path     TEXT PRIMARY KEY,
	size     INTEGER NOT NULL,
	mtime_ns INTEGER NOT NULL,
	offset   INTEGER NOT NULL DEFAULT 0
);
`

// FileState is the stat snapshot of a source file at the time it was last
// successfully scanned. A file whose current stat matches its stored state
// holds no events the database has not already seen.
//
// Size and MtimeNS answer "has this file changed at all"; Offset answers
// "where do we resume". They are deliberately separate: Size is the size
// stat'd at the start of the pass, while Offset is where parsing actually
// stopped, which is earlier whenever the file ended in a partial line.
type FileState struct {
	Size    int64
	MtimeNS int64

	// Offset is the byte position after the last fully-consumed line. Zero
	// means parse from the beginning — the correct default both for files
	// never seen before and for rows written by versions predating resume.
	Offset int64
}

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	// Ensure the directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	// The DSN is a "file:" URI, so Windows paths must use forward slashes.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open usage db: %w", err)
	}
	db.SetMaxOpenConns(1)
	fresh, err := isFreshDatabase(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect usage db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate usage db: %w", err)
	}
	// A fresh database is born at the current version: the schema constant
	// already describes the final shape, so the migration chain only ever
	// runs against databases created by an older binary.
	if fresh {
		err = stampVersion(db)
	} else {
		err = migrate(db)
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate usage db: %w", err)
	}
	return &DB{db: db}, nil
}

func isFreshDatabase(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'usage_events'`).Scan(&count)
	return count == 0, err
}

func stampVersion(db *sql.DB) error {
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, eventSchemaVersion))
	return err
}

// eventSchemaVersion tracks one-time data repairs, recorded in the SQLite
// user_version pragma. Versions 1 and 2 both rekey codex events: v1 dropped
// the source file's full path from the id (archiving moves the file), v2
// dropped file position entirely in favor of session + timestamp + tokens.
// The rekey recomputes from payload, so any older version jumps straight to
// the current scheme in one pass.
// Version 3 adds scanned_files.offset, which lets a scan resume mid-file
// instead of re-parsing every changed file from the beginning. Existing rows
// default to 0, meaning "start over" — correct, just not yet incremental.
//
// Version 4 adds usage_events.lease_until, which lets a claimed batch be
// reclaimed after the process that claimed it died mid-upload.
const eventSchemaVersion = 4

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= eventSchemaVersion {
		return nil
	}
	if version < 2 {
		if err := rekeyCodexEvents(db); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := addColumn(db, `ALTER TABLE scanned_files ADD COLUMN offset INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := addColumn(db, `ALTER TABLE usage_events ADD COLUMN lease_until INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return stampVersion(db)
}

// addColumn runs an ALTER TABLE ADD COLUMN that may already have been applied.
// The schema statement creates fresh databases at the current shape, so a
// column this migration adds can already exist by the time it runs; that
// duplicate-column error is the expected no-op, not a failure.
func addColumn(db *sql.DB, statement string) error {
	_, err := db.Exec(statement)
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

func rekeyCodexEvents(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, payload, status FROM usage_events`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type keeper struct {
		oldID   string
		payload string
		rank    int
	}
	keepers := make(map[string]keeper)
	drop := make([]string, 0)
	for rows.Next() {
		var id, payload, status string
		if err := rows.Scan(&id, &payload, &status); err != nil {
			return err
		}
		var entry usage.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			continue
		}
		if entry.Provider != usage.ProviderCodex {
			continue
		}
		newID := codex.StableEntryID(entry)
		candidate := keeper{oldID: id, payload: payload, rank: statusRank(status)}
		current, exists := keepers[newID]
		switch {
		case !exists:
			keepers[newID] = candidate
		case candidate.rank > current.rank:
			// Rows collapsing onto one id are the same event ingested twice
			// from the file's pre- and post-archive paths; the uploaded copy
			// wins so the duplicate is never re-uploaded.
			drop = append(drop, current.oldID)
			keepers[newID] = candidate
		default:
			drop = append(drop, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range drop {
		if _, err := tx.Exec(`DELETE FROM usage_events WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for newID, kept := range keepers {
		if kept.oldID == newID {
			continue
		}
		var entry usage.Entry
		if err := json.Unmarshal([]byte(kept.payload), &entry); err != nil {
			continue
		}
		entry.ID = newID
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE usage_events SET id = ?, payload = ? WHERE id = ?`, newID, string(payload), kept.oldID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func statusRank(status string) int {
	switch status {
	case "uploaded":
		return 3
	case "rejected":
		return 2
	default: // pending, failed
		return 1
	}
}

func (s *DB) Close() error {
	return s.db.Close()
}

// InsertEvents stores entries with status "pending". Entries whose ID already
// exists are skipped; the number of newly inserted entries is returned.
func (s *DB) InsertEvents(entries []usage.Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO usage_events (id, ts, payload) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	for _, entry := range entries {
		if entry.ID == "" {
			return 0, fmt.Errorf("usage event id is required")
		}
		entry.Language = usage.NormalizeLanguage(entry.Language)
		entry.Project = usage.NormalizeProject(entry.Project)
		payload, err := json.Marshal(entry)
		if err != nil {
			return 0, fmt.Errorf("encode usage event %q: %w", entry.ID, err)
		}
		result, err := stmt.Exec(entry.ID, entry.Timestamp.UTC().Unix(), string(payload))
		if err != nil {
			return 0, fmt.Errorf("save usage event %q: %w", entry.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += int(affected)
	}
	return inserted, tx.Commit()
}

// ScannedFiles returns the stat snapshot of every file recorded as scanned.
func (s *DB) ScannedFiles() (map[string]FileState, error) {
	rows, err := s.db.Query(`SELECT path, size, mtime_ns, offset FROM scanned_files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make(map[string]FileState)
	for rows.Next() {
		var path string
		var state FileState
		if err := rows.Scan(&path, &state.Size, &state.MtimeNS, &state.Offset); err != nil {
			return nil, err
		}
		states[path] = state
	}
	return states, rows.Err()
}

// UpsertScannedFiles records the stat snapshots of files whose events have
// been ingested. Call it only after the corresponding InsertEvents succeeded;
// recording a file before its events are stored would skip them forever.
func (s *DB) UpsertScannedFiles(states map[string]FileState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO scanned_files (path, size, mtime_ns, offset) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for path, state := range states {
		if _, err := stmt.Exec(path, state.Size, state.MtimeNS, state.Offset); err != nil {
			return fmt.Errorf("save scanned file %q: %w", path, err)
		}
	}
	return tx.Commit()
}

// PendingEvents returns events due for upload at now, newest first. A limit
// of zero or less means no limit.
func (s *DB) PendingEvents(now time.Time, limit int) ([]usage.Entry, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query(`
		SELECT payload FROM usage_events
		WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?
		ORDER BY ts DESC, id
		LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var entry usage.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			return nil, fmt.Errorf("decode usage event: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// PendingCount reports how many events are due for upload at now. It is the
// cheap question "is there enough queued to be worth a request", answered
// without claiming anything.
func (s *DB) PendingCount(now time.Time) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT count(*) FROM usage_events
		WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?`, now.Unix()).Scan(&count)
	return count, err
}

// ClaimEvents takes ownership of a batch due for upload and returns it.
//
// Claiming marks the rows "sending" and stamps a lease. Two uploaders running
// at once therefore take different batches instead of both sending the same
// one: the UPDATE is atomic, so whichever runs second sees the rows already
// claimed and moves past them.
//
// Fresh work is claimed first. Only when there is none does it fall back to
// batches whose lease has expired — rows left "sending" by a process that
// died mid-upload. Keeping that fallback off the common path means a healthy
// queue never pays for it, and it also means a slow-but-alive uploader is not
// raced for its batch while ordinary work is still available.
//
// A duplicate send is harmless if it happens anyway: the server dedupes on
// event id and SyncPending counts a duplicate as uploaded. This is why the
// lease can be a plain timestamp rather than something that must be renewed.
func (s *DB) ClaimEvents(now time.Time, limit int, lease time.Duration) ([]usage.Entry, error) {
	if limit <= 0 {
		limit = -1
	}
	leaseUntil := now.Add(lease).Unix()

	entries, err := s.claim(`
		UPDATE usage_events SET status = 'sending', lease_until = ?
		WHERE id IN (
			SELECT id FROM usage_events
			WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?
			ORDER BY ts DESC, id
			LIMIT ?
		)
		RETURNING payload`, leaseUntil, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) >= limit {
		return entries, nil
	}

	// Room left in this batch. Anything still "sending" past its lease belongs
	// to a process that is gone, so reclaiming it is the only way those events
	// are ever sent. Topping up rather than only checking when fresh work runs
	// out matters: on a machine whose queue never empties, an "only if idle"
	// check would never run and those events would be stranded indefinitely.
	remaining := limit - len(entries)
	if limit <= 0 {
		remaining = limit
	}
	expired, err := s.claim(`
		UPDATE usage_events SET status = 'sending', lease_until = ?
		WHERE id IN (
			SELECT id FROM usage_events
			WHERE status = 'sending' AND lease_until <= ?
			ORDER BY ts DESC, id
			LIMIT ?
		)
		RETURNING payload`, leaseUntil, now.Unix(), remaining)
	if err != nil {
		return nil, err
	}
	return append(entries, expired...), nil
}

func (s *DB) claim(query string, args ...any) ([]usage.Entry, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var entry usage.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			return nil, fmt.Errorf("decode usage event: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// ReleaseClaims returns claimed events to the queue without counting an
// attempt against them.
//
// It undoes a claim that resolved into nothing — a server response that did
// not account for every event it was sent. Those rows are not failures to back
// off from; they were simply never answered for, and the next pass should pick
// them up immediately rather than wait out their lease.
func (s *DB) ReleaseClaims(ids []string) error {
	return s.updateEach(ids, func(stmt *sql.Stmt, id string) error {
		_, err := stmt.Exec(id)
		return err
	}, `UPDATE usage_events SET status = 'pending', lease_until = 0 WHERE id = ? AND status = 'sending'`)
}

// MarkEventsUploaded marks ids as accepted by the server.
func (s *DB) MarkEventsUploaded(ids []string) error {
	now := time.Now().UTC().Unix()
	return s.updateEach(ids, func(stmt *sql.Stmt, id string) error {
		_, err := stmt.Exec(now, id)
		return err
	}, `UPDATE usage_events SET status = 'uploaded', uploaded_at = ?, last_error = '' WHERE id = ?`)
}

// MarkEventsRejected marks events the server refused permanently; they are
// never retried.
func (s *DB) MarkEventsRejected(rejected map[string]string) error {
	if len(rejected) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rejected))
	for id := range rejected {
		ids = append(ids, id)
	}
	return s.updateEach(ids, func(stmt *sql.Stmt, id string) error {
		_, err := stmt.Exec(rejected[id], id)
		return err
	}, `UPDATE usage_events SET status = 'rejected', last_error = ? WHERE id = ?`)
}

// MarkEventsUploadFailed records a failed attempt and schedules the next one
// with exponential backoff computed from the previous attempt count.
func (s *DB) MarkEventsUploadFailed(ids []string, message string) error {
	now := time.Now().UTC().Unix()
	return s.updateEach(ids, func(stmt *sql.Stmt, id string) error {
		_, err := stmt.Exec(now, backoffBaseSeconds, backoffMaxSeconds, message, id)
		return err
	}, `UPDATE usage_events SET
		status = 'failed',
		attempt_count = attempt_count + 1,
		next_attempt_at = ? + min(? << min(attempt_count, 7), ?),
		last_error = ?,
		lease_until = 0
	WHERE id = ?`)
}

// PruneUploaded deletes uploaded events older than before and returns how
// many were removed.
func (s *DB) PruneUploaded(before time.Time) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM usage_events WHERE status = 'uploaded' AND uploaded_at < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *DB) updateEach(ids []string, exec func(*sql.Stmt, string) error, query string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := exec(stmt, id); err != nil {
			return fmt.Errorf("update usage event %q: %w", id, err)
		}
	}
	return tx.Commit()
}
