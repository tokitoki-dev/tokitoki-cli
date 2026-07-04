package agentusage

import (
	"os"
	"strings"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadHermesEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := sqliteDBPaths(paths, "state.db", nil)
	entries := make([]usage.Entry, 0)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadHermesDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, dbEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func loadHermesDatabase(path string) ([]usage.Entry, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, model, billing_provider, started_at, message_count, input_tokens,
		       output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
		       estimated_cost_usd, actual_cost_usd
		FROM sessions
		WHERE model IS NOT NULL AND TRIM(model) != ''
	`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var sessionID, model, provider, startedAt, messageCount, input, output, cacheRead, cacheWrite, reasoning, estimatedCost, actualCost any
		if !scanAny(rows, &sessionID, &model, &provider, &startedAt, &messageCount, &input, &output, &cacheRead, &cacheWrite, &reasoning, &estimatedCost, &actualCost) {
			continue
		}
		entry, ok := hermesRowEntry(path, sessionID, model, startedAt, input, output, cacheRead, cacheWrite, reasoning)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func hermesRowEntry(path string, sessionRaw, modelRaw, startedAt, input, output, cacheRead, cacheWrite, reasoning any) (usage.Entry, bool) {
	sessionID := sqlString(sessionRaw)
	model := strings.TrimSpace(sqlString(modelRaw))
	if sessionID == "" || model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := hermesTimestamp(startedAt)
	if !ok {
		return usage.Entry{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              sqlUint(input),
		OutputTokens:             sqlUint(output),
		CacheCreationInputTokens: sqlUint(cacheWrite),
		CacheReadInputTokens:     sqlUint(cacheRead),
		ReasoningOutputTokens:    sqlUint(reasoning),
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = totalUsage(tokens)
	}
	if !nonZero(tokens) {
		return usage.Entry{}, false
	}
	entry := baseEntry(usage.ProviderHermes, timestamp, "hermes", "Hermes", sessionID, model, "Hermes Agent", tokens)
	setSource(&entry, path, 0, 0, 0)
	entry.ID = stableEntryID(entry, "hermes:"+sessionID)
	return entry, true
}

func hermesTimestamp(value any) (time.Time, bool) {
	if parsed, ok := parseTimestamp(value); ok {
		return parsed, true
	}
	if number, ok := sqlFloat(value); ok {
		return timestampFromFloat(number)
	}
	if text := sqlString(value); text != "" {
		return parseTimestampString(text)
	}
	return time.Time{}, false
}

func existingSQLiteFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
