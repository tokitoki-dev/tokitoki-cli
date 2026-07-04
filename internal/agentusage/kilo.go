package agentusage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadKiloEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := sqliteDBPaths(paths, "kilo.db", nil)
	entries := make([]usage.Entry, 0)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadKiloDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, dbEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func loadKiloDatabase(path string) ([]usage.Entry, error) {
	db, err := openSQLite(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, session_id, data FROM message")
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var rowID, rowSessionID, data string
		if err := rows.Scan(&rowID, &rowSessionID, &data); err != nil {
			continue
		}
		if entry, ok := kiloMessageEntry(path, rowID, rowSessionID, data); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func kiloMessageEntry(dbPath, rowID, rowSessionID, data string) (usage.Entry, bool) {
	record := decodeJSONObjectString(data)
	if record == nil || stringField(record, "role") != "assistant" {
		return usage.Entry{}, false
	}
	tokenBlock := objectAt(record["tokens"])
	if tokenBlock == nil {
		return usage.Entry{}, false
	}
	cache := objectAt(tokenBlock["cache"])
	tokens := usage.TokenUsage{
		InputTokens:              uintField(tokenBlock, "input"),
		OutputTokens:             uintField(tokenBlock, "output"),
		CacheCreationInputTokens: uintField(cache, "write"),
		CacheReadInputTokens:     uintField(cache, "read"),
		ReasoningOutputTokens:    uintField(tokenBlock, "reasoning"),
	}
	tokens = applyTotalFallback(tokens, uintField(tokenBlock, "total"))
	if !nonZero(tokens) {
		return usage.Entry{}, false
	}
	model := stringField(record, "modelID")
	if model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := parseTimestamp(objectAt(record["time"])["created"])
	if !ok {
		return usage.Entry{}, false
	}
	sessionID := firstNonEmpty(stringField(record, "session_id"), rowSessionID, "unknown")
	messageID := firstNonEmpty(stringField(record, "id"), rowID)
	entry := baseEntry(usage.ProviderKilo, timestamp, "kilo", "Kilo", sessionID, model, "Kilo", tokens)
	setSource(&entry, dbPath, 0, 0, 0)
	entry.ID = stableEntryID(entry, messageID)
	return entry, true
}

func decodeJSONObjectString(data string) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil
	}
	return record
}

func sqliteDBPaths(paths []string, defaultFile string, extraNames func(string) bool) []string {
	dbPaths := make([]string, 0)
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if filepath.Base(root) == defaultFile || extraNames != nil && extraNames(filepath.Base(root)) {
				dbPaths = append(dbPaths, root)
			}
			continue
		}
		candidate := filepath.Join(root, defaultFile)
		if fileInfo, err := os.Stat(candidate); err == nil && !fileInfo.IsDir() {
			dbPaths = append(dbPaths, candidate)
		}
		if extraNames == nil {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !extraNames(entry.Name()) {
				continue
			}
			dbPaths = append(dbPaths, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(dbPaths)
	return uniqueStrings(dbPaths)
}

func scanAny(rows *sql.Rows, values ...*any) bool {
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = values[i]
	}
	return rows.Scan(dest...) == nil
}
