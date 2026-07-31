package kilo

import (
	"errors"
	"os"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := agentdb.SqliteDBPaths(paths, "kilo.db", nil)
	entries := make([]usage.Entry, 0)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, dbEntries...)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

func loadDatabase(path string) ([]usage.Entry, error) {
	db, err := agentdb.OpenSQLite(path)
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
	record := agentdata.DecodeJSONObjectString(data)
	if record == nil || agentdata.StringField(record, "role") != "assistant" {
		return usage.Entry{}, false
	}
	tokenBlock := agentdata.ObjectAt(record["tokens"])
	if tokenBlock == nil {
		return usage.Entry{}, false
	}
	cache := agentdata.ObjectAt(tokenBlock["cache"])
	tokens := usage.TokenUsage{
		InputTokens:              agentdata.UintField(tokenBlock, "input"),
		OutputTokens:             agentdata.UintField(tokenBlock, "output"),
		CacheCreationInputTokens: agentdata.UintField(cache, "write"),
		CacheReadInputTokens:     agentdata.UintField(cache, "read"),
		ReasoningOutputTokens:    agentdata.UintField(tokenBlock, "reasoning"),
	}
	tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(tokenBlock, "total"))
	if !usageprovider.NonZero(tokens) {
		return usage.Entry{}, false
	}
	model := agentdata.StringField(record, "modelID")
	if model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := agentdata.ParseTimestamp(agentdata.ObjectAt(record["time"])["created"])
	if !ok {
		return usage.Entry{}, false
	}
	sessionID := agentdata.FirstNonEmpty(agentdata.StringField(record, "session_id"), rowSessionID, "unknown")
	messageID := agentdata.FirstNonEmpty(agentdata.StringField(record, "id"), rowID)
	entry := usageprovider.BaseEntry(usage.ProviderKilo, timestamp, "kilo", "Kilo", sessionID, model, "Kilo", tokens)
	usageprovider.SetSource(&entry, dbPath, 0, 0, 0)
	entry.ID = usageprovider.StableEntryID(entry, messageID)
	return entry, true
}
