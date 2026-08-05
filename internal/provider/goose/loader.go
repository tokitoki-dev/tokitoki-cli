package goose

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string) ([]usage.Entry, error) {
	dbPaths := dbPaths(paths)
	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, dbPath := range dbPaths {
		dbEntries, err := loadDatabase(dbPath)
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
	usageprovider.SortEntries(entries)
	return entries, nil
}

func dbPaths(paths []string) []string {
	dbPaths := make([]string, 0)
	for _, root := range paths {
		if agentdb.ExistingSQLiteFile(root) {
			dbPaths = append(dbPaths, root)
			continue
		}
		for _, candidate := range []string{
			filepath.Join(root, "sessions.db"),
			filepath.Join(root, "sessions", "sessions.db"),
			filepath.Join(root, "data", "sessions", "sessions.db"),
		} {
			if agentdb.ExistingSQLiteFile(candidate) {
				dbPaths = append(dbPaths, candidate)
			}
		}
	}
	sort.Strings(dbPaths)
	return agentdata.UniqueStrings(dbPaths)
}

func loadDatabase(path string) ([]usage.Entry, error) {
	db, err := agentdb.OpenSQLite(path)
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
		if !agentdb.ScanAny(rows, &id, &modelConfig, &providerName, &createdAt, &total, &input, &output, &accumulatedTotal, &accumulatedInput, &accumulatedOutput) {
			continue
		}
		if entry, ok := rowEntry(path, id, modelConfig, providerName, createdAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func rowEntry(path string, id, modelConfig, providerName, createdAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput any) (usage.Entry, bool) {
	sessionID := agentdb.SqlString(id)
	model := modelName(agentdb.SqlString(modelConfig))
	if sessionID == "" || model == "" {
		return usage.Entry{}, false
	}
	timestamp, ok := timestamp(agentdb.SqlString(createdAt))
	if !ok {
		return usage.Entry{}, false
	}
	inputTokens := firstPositive(agentdb.SqlUint(accumulatedInput), agentdb.SqlUint(input))
	outputTokens := firstPositive(agentdb.SqlUint(accumulatedOutput), agentdb.SqlUint(output))
	totalTokens := firstPositive(agentdb.SqlUint(accumulatedTotal), agentdb.SqlUint(total), inputTokens+outputTokens)
	tokens := usage.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	if totalTokens > inputTokens+outputTokens {
		tokens.ReasoningOutputTokens = totalTokens - inputTokens - outputTokens
	}
	tokens.TotalTokens = usageprovider.TotalUsage(tokens)
	if !usageprovider.NonZero(tokens) {
		return usage.Entry{}, false
	}
	entry := usageprovider.BaseEntry(usage.ProviderGoose, timestamp, "goose", "Goose", sessionID, model, "Goose", tokens)
	usageprovider.SetSource(&entry, path, 0, 0, 0)
	entry.ID = usageprovider.StableEntryID(entry, "goose:"+sessionID+":"+agentdb.SqlString(providerName))
	return entry, true
}

func modelName(config string) string {
	record := agentdata.DecodeJSONObjectString(config)
	return agentdata.StringField(record, "model_name")
}

func timestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if timestamp, ok := agentdata.ParseTimestampString(value); ok {
		return timestamp, true
	}
	if len(value) == 19 && value[4] == '-' && value[7] == '-' && (value[10] == ' ' || value[10] == 'T') {
		return agentdata.ParseTimestampString(value[:10] + "T" + value[11:] + "Z")
	}
	if len(value) == 10 && value[4] == '-' && value[7] == '-' {
		return agentdata.ParseTimestampString(value + "T00:00:00Z")
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
