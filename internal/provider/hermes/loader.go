package hermes

import (
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := shared.SqliteDBPaths(paths, "state.db", nil)
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
		if !shared.ScanAny(rows, &sessionID, &model, &provider, &startedAt, &messageCount, &input, &output, &cacheRead, &cacheWrite, &reasoning, &estimatedCost, &actualCost) {
			continue
		}
		entry, ok := rowEntry(path, sessionID, model, startedAt, input, output, cacheRead, cacheWrite, reasoning)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func rowEntry(path string, sessionRaw, modelRaw, startedAt, input, output, cacheRead, cacheWrite, reasoning any) (usage.Entry, bool) {
	sessionID := shared.SqlString(sessionRaw)
	model := strings.TrimSpace(shared.SqlString(modelRaw))
	if sessionID == "" || model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := timestamp(startedAt)
	if !ok {
		return usage.Entry{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              shared.SqlUint(input),
		OutputTokens:             shared.SqlUint(output),
		CacheCreationInputTokens: shared.SqlUint(cacheWrite),
		CacheReadInputTokens:     shared.SqlUint(cacheRead),
		ReasoningOutputTokens:    shared.SqlUint(reasoning),
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = shared.TotalUsage(tokens)
	}
	if !shared.NonZero(tokens) {
		return usage.Entry{}, false
	}
	entry := shared.BaseEntry(usage.ProviderHermes, timestamp, "hermes", "Hermes", sessionID, model, "Hermes Agent", tokens)
	shared.SetSource(&entry, path, 0, 0, 0)
	entry.ID = shared.StableEntryID(entry, "hermes:"+sessionID)
	return entry, true
}

func timestamp(value any) (time.Time, bool) {
	if parsed, ok := shared.ParseTimestamp(value); ok {
		return parsed, true
	}
	if number, ok := shared.SqlFloat(value); ok {
		return shared.TimestampFromFloat(number)
	}
	if text := shared.SqlString(value); text != "" {
		return shared.ParseTimestampString(text)
	}
	return time.Time{}, false
}
