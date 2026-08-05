package copilot

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// The Copilot CLI stopped exporting OpenTelemetry files and now records every
// API call in ~/.copilot/session-store.db: assistant_usage_events holds the
// per-call token counts and sessions holds the working directory and branch
// the session ran in.

// storeSession is one row of the sessions table: where a session ran.
type storeSession struct {
	cwd        string
	repository string
	branch     string
}

// storeEvent is one row of assistant_usage_events: one API call's usage.
type storeEvent struct {
	sessionID string
	turnKey   string
	model     string
	timestamp time.Time
	tokens    usage.TokenUsage
}

func loadStoreDatabase(path string) ([]storeEvent, map[string]storeSession, error) {
	db, err := agentdb.OpenSQLite(path)
	if err != nil {
		// A missing or unreadable store is not an error: the CLI version in
		// use may simply predate it.
		return nil, nil, nil
	}
	defer db.Close()

	sessions := queryStoreSessions(db)
	events := queryStoreEvents(db)
	return events, sessions, nil
}

func queryStoreSessions(db *sql.DB) map[string]storeSession {
	sessions := make(map[string]storeSession)
	rows, err := db.Query(`SELECT id, COALESCE(cwd, ''), COALESCE(repository, ''), COALESCE(branch, '') FROM sessions`)
	if err != nil {
		return sessions
	}
	defer rows.Close()
	for rows.Next() {
		var id, cwd, repository, branch string
		if err := rows.Scan(&id, &cwd, &repository, &branch); err != nil {
			continue
		}
		sessions[id] = storeSession{
			cwd:        strings.TrimSpace(cwd),
			repository: strings.TrimSpace(repository),
			branch:     strings.TrimSpace(branch),
		}
	}
	return sessions
}

func queryStoreEvents(db *sql.DB) []storeEvent {
	rows, err := db.Query(`
		SELECT
			session_id,
			COALESCE(turn_index, -1),
			model,
			COALESCE(input_tokens, 0),
			COALESCE(output_tokens, 0),
			COALESCE(cache_read_tokens, 0),
			COALESCE(cache_write_tokens, 0),
			COALESCE(reasoning_tokens, 0),
			COALESCE(token_details_json, ''),
			COALESCE(created_at, '')
		FROM assistant_usage_events
		ORDER BY id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	events := make([]storeEvent, 0)
	for rows.Next() {
		var sessionID, model, details, createdAt string
		var turnIndex, input, output, cacheRead, cacheWrite, reasoning int64
		if err := rows.Scan(&sessionID, &turnIndex, &model, &input, &output, &cacheRead, &cacheWrite, &reasoning, &details, &createdAt); err != nil {
			continue
		}
		timestamp, ok := parseStoreTimestamp(createdAt)
		if !ok {
			continue
		}
		tokens := normalizeStoreTokens(input, output, cacheRead, cacheWrite, reasoning, details)
		if !nonZeroTokens(tokens) {
			continue
		}
		turnKey := ""
		if turnIndex >= 0 {
			turnKey = strconv.FormatInt(turnIndex, 10)
		}
		events = append(events, storeEvent{
			sessionID: strings.TrimSpace(sessionID),
			turnKey:   turnKey,
			model:     strings.TrimSpace(model),
			timestamp: timestamp,
			tokens:    tokens,
		})
	}
	return events
}

// normalizeStoreTokens splits the raw counters into disjoint buckets. The
// store's input_tokens includes the cached reads and writes, and its
// output_tokens includes the reasoning tokens, so summing the raw columns
// would bill the overlaps twice. token_details_json carries the exact split
// when it agrees with the raw totals; otherwise the overlaps are subtracted.
func normalizeStoreTokens(input, output, cacheRead, cacheWrite, reasoning int64, detailsJSON string) usage.TokenUsage {
	inputRaw := clampUint(input)
	outputRaw := clampUint(output)
	cacheReadRaw := clampUint(cacheRead)
	cacheWriteRaw := clampUint(cacheWrite)
	reasoningRaw := clampUint(reasoning)

	tokens := usage.TokenUsage{}
	if details, ok := parseTokenDetails(detailsJSON); ok &&
		details.input+details.cacheRead+details.cacheWrite == inputRaw &&
		details.output == outputRaw {
		tokens.InputTokens = details.input
		tokens.CacheReadInputTokens = details.cacheRead
		tokens.CacheCreationInputTokens = details.cacheWrite
		tokens.OutputTokens = details.output
	} else {
		read := min(cacheReadRaw, inputRaw)
		write := min(cacheWriteRaw, inputRaw-read)
		tokens.InputTokens = inputRaw - read - write
		tokens.CacheReadInputTokens = read
		tokens.CacheCreationInputTokens = write
		tokens.OutputTokens = outputRaw
	}

	tokens.ReasoningOutputTokens = min(reasoningRaw, tokens.OutputTokens)
	tokens.OutputTokens -= tokens.ReasoningOutputTokens
	tokens.TotalTokens = tokens.InputTokens +
		tokens.CacheReadInputTokens +
		tokens.CacheCreationInputTokens +
		tokens.OutputTokens +
		tokens.ReasoningOutputTokens
	return tokens
}

type tokenDetails struct {
	input, output, cacheRead, cacheWrite uint64
}

// parseTokenDetails reads token_details_json, a list of
// {"tokenType": "input", "tokenCount": 123, ...} objects.
func parseTokenDetails(raw string) (tokenDetails, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tokenDetails{}, false
	}
	var rows []struct {
		TokenType  string      `json:"tokenType"`
		TokenCount json.Number `json:"tokenCount"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) == 0 {
		return tokenDetails{}, false
	}
	details := tokenDetails{}
	for _, row := range rows {
		count := agentdata.UintValue(row.TokenCount)
		switch row.TokenType {
		case "input":
			details.input += count
		case "output":
			details.output += count
		case "cache_read":
			details.cacheRead += count
		case "cache_write", "cache_creation":
			details.cacheWrite += count
		default:
			// An unknown bucket means the sum check below cannot be trusted.
			return tokenDetails{}, false
		}
	}
	return details, true
}

func parseStoreTimestamp(raw string) (time.Time, bool) {
	if timestamp, ok := agentdata.ParseTimestampString(raw); ok {
		return timestamp, true
	}
	// SQLite's datetime('now') default writes "2006-01-02 15:04:05" in UTC.
	if timestamp, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(raw)); err == nil {
		return timestamp.UTC(), true
	}
	return time.Time{}, false
}

func clampUint(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}

func nonZeroTokens(tokens usage.TokenUsage) bool {
	return tokens.InputTokens+tokens.OutputTokens+
		tokens.CacheReadInputTokens+tokens.CacheCreationInputTokens+
		tokens.ReasoningOutputTokens > 0
}
