// Package usagedb persists usage events and their upload queue state in a
// local SQLite database. Events are written first with status "pending" and
// uploaded afterwards; failed uploads back off exponentially so an offline
// machine retries calmly instead of hammering the network on every heartbeat.
package usagedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

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
	last_error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_usage_events_queue ON usage_events(status, next_attempt_at);
`

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	// The DSN is a "file:" URI, so Windows paths must use forward slashes.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open usage db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate usage db: %w", err)
	}
	return &DB{db: db}, nil
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

// PendingEvents returns events due for upload at now, oldest first. A limit
// of zero or less means no limit.
func (s *DB) PendingEvents(now time.Time, limit int) ([]usage.Entry, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query(`
		SELECT payload FROM usage_events
		WHERE status IN ('pending', 'failed') AND next_attempt_at <= ?
		ORDER BY ts, id
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
		last_error = ?
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
