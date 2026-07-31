package kilo

import (
	"errors"
	"os"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := shared.SqliteDBPaths(paths, "kilo.db", nil)
	entries := make([]usage.Entry, 0)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, dbEntries...)
	}
	shared.SortEntries(entries)
	return entries, nil
}

func loadDatabase(path string) ([]usage.Entry, error) {
	db, err := shared.OpenSQLite(path)
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
		if entry, ok := messageEntry(path, rowID, rowSessionID, data); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func messageEntry(dbPath, rowID, rowSessionID, data string) (usage.Entry, bool) {
	record := shared.DecodeJSONObjectString(data)
	if record == nil || shared.StringField(record, "role") != "assistant" {
		return usage.Entry{}, false
	}
	tokenBlock := shared.ObjectAt(record["tokens"])
	if tokenBlock == nil {
		return usage.Entry{}, false
	}
	cache := shared.ObjectAt(tokenBlock["cache"])
	tokens := usage.TokenUsage{
		InputTokens:              shared.UintField(tokenBlock, "input"),
		OutputTokens:             shared.UintField(tokenBlock, "output"),
		CacheCreationInputTokens: shared.UintField(cache, "write"),
		CacheReadInputTokens:     shared.UintField(cache, "read"),
		ReasoningOutputTokens:    shared.UintField(tokenBlock, "reasoning"),
	}
	tokens = shared.ApplyTotalFallback(tokens, shared.UintField(tokenBlock, "total"))
	if !shared.NonZero(tokens) {
		return usage.Entry{}, false
	}
	model := shared.StringField(record, "modelID")
	if model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := shared.ParseTimestamp(shared.ObjectAt(record["time"])["created"])
	if !ok {
		return usage.Entry{}, false
	}
	sessionID := shared.FirstNonEmpty(shared.StringField(record, "session_id"), rowSessionID, "unknown")
	messageID := shared.FirstNonEmpty(shared.StringField(record, "id"), rowID)
	entry := shared.BaseEntry(usage.ProviderKilo, timestamp, "kilo", "Kilo", sessionID, model, "Kilo", tokens)
	shared.SetSource(&entry, dbPath, 0, 0, 0)
	entry.ID = shared.StableEntryID(entry, messageID)
	return entry, true
}
