package goose

import (
	"database/sql"
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

	// Older Goose databases predate sessions.updated_at and working_dir.
	// Selecting NULL in their place gives every schema the same row shape.
	rows, err := db.Query(`
		SELECT id, model_config_json, created_at, ` + optionalColumn(db, "updated_at") + `,
		       total_tokens, input_tokens, output_tokens, accumulated_total_tokens,
		       accumulated_input_tokens, accumulated_output_tokens, ` + optionalColumn(db, "working_dir") + `
		FROM sessions
		WHERE model_config_json IS NOT NULL AND TRIM(model_config_json) != ''
	`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	entries := make([]usage.Entry, 0)
	for rows.Next() {
		var id, modelConfig, createdAt, updatedAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput, cwd any
		if !agentdb.ScanAny(rows, &id, &modelConfig, &createdAt, &updatedAt, &total, &input, &output, &accumulatedTotal, &accumulatedInput, &accumulatedOutput, &cwd) {
			continue
		}
		if entry, ok := rowEntry(path, id, modelConfig, createdAt, updatedAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput, cwd); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

// optionalColumn selects a sessions column older databases lack, or NULL
// where it is missing. A failed lookup reads as missing, so the scan falls
// back to the older shape instead of failing.
func optionalColumn(db *sql.DB, column string) string {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`, column).Scan(&n); err != nil || n == 0 {
		return "NULL"
	}
	return column
}

// rowEntry reads one session's running total (see Provider.ReportsRunningTotals).
func rowEntry(path string, id, modelConfig, createdAt, updatedAt, total, input, output, accumulatedTotal, accumulatedInput, accumulatedOutput, cwd any) (usage.Entry, bool) {
	sessionID := agentdb.SqlString(id)
	model := modelName(agentdb.SqlString(modelConfig))
	if sessionID == "" || model == "" {
		return usage.Entry{}, false
	}
	// When the session last moved, which is when its growth since the
	// previous read happened; its start on databases that do not say.
	at, ok := timestamp(agentdb.SqlString(updatedAt))
	if !ok {
		at, ok = timestamp(agentdb.SqlString(createdAt))
	}
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
	projectPath := "Goose"
	if dir, _, ok := usage.ProjectFromCWD(agentdb.SqlString(cwd)); ok {
		projectPath = dir
	}
	entry := usageprovider.BaseEntry(usage.ProviderGoose, at, "goose", projectPath, sessionID, model, "Goose", tokens)
	usageprovider.SetSource(&entry, path, 0, 0, 0)
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
