package usagedb

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
	bolt "go.etcd.io/bbolt"
)

var (
	usageEventsBucket  = []byte("usage_events")
	uploadStatesBucket = []byte("upload_states")
	sourceFilesBucket  = []byte("source_files")
)

type DB struct {
	db *bolt.DB
}

type UploadStatus string

const (
	UploadPending  UploadStatus = "pending"
	UploadFailed   UploadStatus = "failed"
	UploadUploaded UploadStatus = "uploaded"
	UploadRejected UploadStatus = "rejected"
)

type UploadState struct {
	EventID       string       `json:"event_id"`
	Status        UploadStatus `json:"status"`
	AttemptCount  int          `json:"attempt_count"`
	LastAttemptAt *time.Time   `json:"last_attempt_at,omitempty"`
	UploadedAt    *time.Time   `json:"uploaded_at,omitempty"`
	LastError     string       `json:"last_error,omitempty"`
}

type SourceFile struct {
	Provider      usage.Provider `json:"provider"`
	Path          string         `json:"path"`
	Size          int64          `json:"size"`
	ModTimeUnixNS int64          `json:"mtime_unix_ns"`
	ScannedAt     time.Time      `json:"scanned_at"`
	LastError     string         `json:"last_error,omitempty"`
}

type ScanResult struct {
	FilesSeen      int `json:"files_seen"`
	FilesScanned   int `json:"files_scanned"`
	FilesSkipped   int `json:"files_skipped"`
	EventsParsed   int `json:"events_parsed"`
	EventsInserted int `json:"events_inserted"`
}

func Open(path string) (*DB, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open usage db: %w", err)
	}
	store := &DB{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) migrate() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(usageEventsBucket); err != nil {
			return fmt.Errorf("create usage events bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists(uploadStatesBucket); err != nil {
			return fmt.Errorf("create upload states bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists(sourceFilesBucket); err != nil {
			return fmt.Errorf("create source files bucket: %w", err)
		}
		return nil
	})
}

func (s *DB) SourceFile(provider usage.Provider, path string) (SourceFile, bool, error) {
	var source SourceFile
	key := sourceFileKey(provider, path)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(sourceFilesBucket)
		data := bucket.Get([]byte(key))
		if data == nil {
			return nil
		}
		if err := json.Unmarshal(data, &source); err != nil {
			return fmt.Errorf("decode source file %q: %w", key, err)
		}
		return nil
	})
	if err != nil {
		return SourceFile{}, false, err
	}
	if source.Path == "" {
		return SourceFile{}, false, nil
	}
	return source, true, nil
}

func (s *DB) SaveSourceFile(source SourceFile) error {
	data, err := json.Marshal(source)
	if err != nil {
		return fmt.Errorf("encode source file: %w", err)
	}
	key := sourceFileKey(source.Provider, source.Path)
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(sourceFilesBucket)
		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("save source file %q: %w", key, err)
		}
		return nil
	})
}

func (s *DB) InsertEvents(entries []usage.Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	return insertEvents(s.db, entries)
}

func (s *DB) UsageEvents() ([]usage.Entry, error) {
	entries := make([]usage.Entry, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(usageEventsBucket)
		return bucket.ForEach(func(_, data []byte) error {
			var entry usage.Entry
			if err := json.Unmarshal(data, &entry); err != nil {
				return fmt.Errorf("decode usage event: %w", err)
			}
			entry.Language = usage.NormalizeLanguage(entry.Language)
			entries = append(entries, entry)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *DB) PendingUsageEvents(limit int) ([]usage.Entry, error) {
	entries := make([]usage.Entry, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		events := tx.Bucket(usageEventsBucket)
		states := tx.Bucket(uploadStatesBucket)
		return events.ForEach(func(key, data []byte) error {
			if limit > 0 && len(entries) >= limit {
				return nil
			}
			state, err := decodeUploadState(states.Get(key))
			if err != nil {
				return fmt.Errorf("decode upload state %q: %w", string(key), err)
			}
			if state.Status == UploadUploaded || state.Status == UploadRejected {
				return nil
			}
			var entry usage.Entry
			if err := json.Unmarshal(data, &entry); err != nil {
				return fmt.Errorf("decode usage event: %w", err)
			}
			entry.Language = usage.NormalizeLanguage(entry.Language)
			entries = append(entries, entry)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *DB) MarkEventsUploaded(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return s.db.Update(func(tx *bolt.Tx) error {
		states := tx.Bucket(uploadStatesBucket)
		for _, id := range ids {
			if id == "" {
				continue
			}
			state, err := decodeUploadState(states.Get([]byte(id)))
			if err != nil {
				return fmt.Errorf("decode upload state %q: %w", id, err)
			}
			state.EventID = id
			state.Status = UploadUploaded
			state.LastAttemptAt = &now
			state.UploadedAt = &now
			state.LastError = ""
			if err := saveUploadState(states, state); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) MarkEventsRejected(rejected map[string]string) error {
	if len(rejected) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return s.db.Update(func(tx *bolt.Tx) error {
		states := tx.Bucket(uploadStatesBucket)
		for id, reason := range rejected {
			if id == "" {
				continue
			}
			state, err := decodeUploadState(states.Get([]byte(id)))
			if err != nil {
				return fmt.Errorf("decode upload state %q: %w", id, err)
			}
			state.EventID = id
			state.Status = UploadRejected
			state.LastAttemptAt = &now
			state.LastError = reason
			if err := saveUploadState(states, state); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) MarkEventsUploadFailed(ids []string, message string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return s.db.Update(func(tx *bolt.Tx) error {
		states := tx.Bucket(uploadStatesBucket)
		for _, id := range ids {
			if id == "" {
				continue
			}
			state, err := decodeUploadState(states.Get([]byte(id)))
			if err != nil {
				return fmt.Errorf("decode upload state %q: %w", id, err)
			}
			state.EventID = id
			state.Status = UploadFailed
			state.AttemptCount++
			state.LastAttemptAt = &now
			state.LastError = message
			if err := saveUploadState(states, state); err != nil {
				return err
			}
		}
		return nil
	})
}

// CountEvents returns the number of indexed usage events without decoding them.
func (s *DB) CountEvents() (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		count = tx.Bucket(usageEventsBucket).Stats().KeyN
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func insertEvents(db *bolt.DB, entries []usage.Entry) (int, error) {
	inserted := 0
	err := db.Update(func(tx *bolt.Tx) error {
		events := tx.Bucket(usageEventsBucket)
		states := tx.Bucket(uploadStatesBucket)
		for _, entry := range entries {
			if entry.ID == "" {
				return fmt.Errorf("usage event id is required")
			}
			entry.Language = usage.NormalizeLanguage(entry.Language)
			key := []byte(entry.ID)
			if events.Get(key) != nil {
				continue
			}
			data, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("encode usage event %q: %w", entry.ID, err)
			}
			if err := events.Put(key, data); err != nil {
				return fmt.Errorf("save usage event %q: %w", entry.ID, err)
			}
			if err := saveUploadState(states, UploadState{EventID: entry.ID, Status: UploadPending}); err != nil {
				return err
			}
			inserted++
		}
		return nil
	})
	return inserted, err
}

func decodeUploadState(data []byte) (UploadState, error) {
	if data == nil {
		return UploadState{Status: UploadPending}, nil
	}
	var state UploadState
	if err := json.Unmarshal(data, &state); err != nil {
		return UploadState{}, err
	}
	if state.Status == "" {
		state.Status = UploadPending
	}
	return state, nil
}

func saveUploadState(bucket *bolt.Bucket, state UploadState) error {
	if state.EventID == "" {
		return fmt.Errorf("upload state event id is required")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode upload state %q: %w", state.EventID, err)
	}
	if err := bucket.Put([]byte(state.EventID), data); err != nil {
		return fmt.Errorf("save upload state %q: %w", state.EventID, err)
	}
	return nil
}

func (s *DB) DailyProjectSummaries(providerFilter, projectFilter string) ([]usage.DailyProjectSummary, error) {
	var entries []usage.Entry
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(usageEventsBucket)
		return bucket.ForEach(func(_, data []byte) error {
			var entry usage.Entry
			if err := json.Unmarshal(data, &entry); err != nil {
				return fmt.Errorf("decode usage event: %w", err)
			}
			entry.Language = usage.NormalizeLanguage(entry.Language)
			if providerFilter != "" && providerFilter != "all" && string(entry.Provider) != providerFilter {
				return nil
			}
			if projectFilter != "" && entry.Project != projectFilter && entry.ProjectPath != projectFilter {
				return nil
			}
			entries = append(entries, entry)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	summaries := usage.SummarizeDailyProjects(entries)
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Provider != summaries[j].Provider {
			return summaries[i].Provider < summaries[j].Provider
		}
		if summaries[i].Project != summaries[j].Project {
			return summaries[i].Project < summaries[j].Project
		}
		return summaries[i].Date < summaries[j].Date
	})
	return summaries, nil
}

func FileSource(provider usage.Provider, path string, info os.FileInfo) SourceFile {
	return SourceFile{
		Provider:      provider,
		Path:          path,
		Size:          info.Size(),
		ModTimeUnixNS: info.ModTime().UnixNano(),
		ScannedAt:     time.Now().UTC(),
	}
}

func sourceFileKey(provider usage.Provider, path string) string {
	return string(provider) + "\x00" + path
}
