package agentusage

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadGooseEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := gooseDBPaths(paths)
	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadGooseDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range dbEntries {
			key := dbPath + ":" + entry.SessionID
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, entry)
		}
	}
	sortEntries(entries)
	return entries, nil
}

func gooseDBPaths(paths []string) []string {
	dbPaths := make([]string, 0)
	for _, root := range paths {
		if existingSQLiteFile(root) {
			dbPaths = append(dbPaths, root)
			continue
		}
		for _, candidate := range []string{
			filepath.Join(root, "sessions.db"),
			filepath.Join(root, "sessions", "sessions.db"),
			filepath.Join(root, "data", "sessions", "sessions.db"),
		} {
			if existingSQLiteFile(candidate) {
				dbPaths = append(dbPaths, candidate)
			}
		}
	}
	sort.Strings(dbPaths)
	return uniqueStrings(dbPaths)
}

func loadGooseDatabase(path string) ([]usage.Entry, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, model_config_json, provider_name, created_at, total_tokens,
		       input_tokens, output_tokens, accumulated_total_tokens,
		       accumulated_input_tokens, accumulated_output_tokens
		FROM sessions
		WHERE model_config_json IS NOT NULL AND TRIM(model_config_json) != ''
	`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var id, modelConfig, providerName, createdAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput any
		if !scanAny(rows, &id, &modelConfig, &providerName, &createdAt, &total, &input, &output, &accumulatedTotal, &accumulatedInput, &accumulatedOutput) {
			continue
		}
		if entry, ok := gooseRowEntry(path, id, modelConfig, providerName, createdAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func gooseRowEntry(path string, id, modelConfig, providerName, createdAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput any) (usage.Entry, bool) {
	sessionID := sqlString(id)
	model := gooseModelName(sqlString(modelConfig))
	if sessionID == "" || model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := gooseTimestamp(sqlString(createdAt))
	if !ok {
		return usage.Entry{}, false
	}
	inputTokens := firstPositive(sqlUint(accumulatedInput), sqlUint(input))
	outputTokens := firstPositive(sqlUint(accumulatedOutput), sqlUint(output))
	totalTokens := firstPositive(sqlUint(accumulatedTotal), sqlUint(total), inputTokens+outputTokens)
	tokens := usage.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	if totalTokens > inputTokens+outputTokens {
		tokens.ReasoningOutputTokens = totalTokens - inputTokens - outputTokens
	}
	tokens.TotalTokens = totalUsage(tokens)
	if !nonZero(tokens) {
		return usage.Entry{}, false
	}
	entry := baseEntry(usage.ProviderGoose, timestamp, "goose", "Goose", sessionID, model, "Goose", tokens)
	setSource(&entry, path, 0, 0, 0)
	entry.ID = stableEntryID(entry, "goose:"+sessionID+":"+sqlString(providerName))
	return entry, true
}

func gooseModelName(config string) string {
	record := decodeJSONObjectString(config)
	return stringField(record, "model_name")
}

func gooseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if timestamp, ok := parseTimestampString(value); ok {
		return timestamp, true
	}
	if len(value) == 19 && value[4] == '-' && value[7] == '-' && (value[10] == ' ' || value[10] == 'T') {
		return parseTimestampString(value[:10] + "T" + value[11:] + "Z")
	}
	if len(value) == 10 && value[4] == '-' && value[7] == '-' {
		return parseTimestampString(value + "T00:00:00Z")
	}
	return time.Time{}, false
}

func firstPositive(values ...uint64) uint64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
