package agentusage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadOpenCodeEntries(paths []string) ([]usage.Entry, error) {
	entriesByID := make(map[string]usage.Entry)
	for _, root := range paths {
		rootEntries, err := loadOpenCodeRoot(root)
		if err != nil {
			return nil, err
		}
		for _, entry := range rootEntries {
			if entry.ID == "" {
				entry.ID = stableEntryID(entry)
			}
			if _, exists := entriesByID[entry.ID]; !exists {
				entriesByID[entry.ID] = entry
			}
		}
	}
	entries := make([]usage.Entry, 0, len(entriesByID))
	for _, entry := range entriesByID {
		entries = append(entries, entry)
	}
	sortEntries(entries)
	return entries, nil
}

func loadOpenCodeRoot(root string) ([]usage.Entry, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil
	}
	if !info.IsDir() {
		switch strings.ToLower(filepath.Ext(root)) {
		case ".db":
			return loadOpenCodeDatabase(root)
		case ".json":
			if entry, ok, err := parseOpenCodeMessageFile(root, "", ""); err != nil || ok {
				if !ok {
					return nil, err
				}
				return []usage.Entry{entry}, err
			}
		}
		return nil, nil
	}

	entries := make([]usage.Entry, 0)
	seenIDs := make(map[string]bool)
	if dbPath := openCodeDBPath(root); dbPath != "" {
		dbEntries, err := loadOpenCodeDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range dbEntries {
			if entry.ID != "" {
				seenIDs[entry.ID] = true
			}
			entries = append(entries, entry)
		}
	}

	files := collectExt(filepath.Join(root, "storage", "message"), ".json")
	sort.Strings(files)
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if seenIDs[stableOpenCodeMessageID(stem)] {
			continue
		}
		entry, ok, err := parseOpenCodeMessageFile(file, "", "")
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if entry.ID != "" && seenIDs[entry.ID] {
			continue
		}
		if entry.ID != "" {
			seenIDs[entry.ID] = true
		}
		entries = append(entries, entry)
	}
	sortEntries(entries)
	return entries, nil
}

func openCodeDBPath(root string) string {
	candidate := filepath.Join(root, "opencode.db")
	if existingSQLiteFile(candidate) {
		return candidate
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	candidates := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isOpenCodeChannelDB(name) {
			continue
		}
		candidates = append(candidates, filepath.Join(root, name))
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func isOpenCodeChannelDB(name string) bool {
	if !strings.HasPrefix(name, "opencode-") || !strings.HasSuffix(name, ".db") {
		return false
	}
	channel := strings.TrimSuffix(strings.TrimPrefix(name, "opencode-"), ".db")
	if channel == "" {
		return false
	}
	for _, ch := range channel {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func loadOpenCodeDatabase(path string) ([]usage.Entry, error) {
	db, err := openSQLite(path)
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
		if entry, ok := openCodeMessageEntry(path, decodeJSONObjectString(data), rowID, rowSessionID, 0); ok {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func parseOpenCodeMessageFile(path, rowID, rowSessionID string) (usage.Entry, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return usage.Entry{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return usage.Entry{}, false, nil
	}
	entry, ok := openCodeMessageEntry(path, record, rowID, rowSessionID, 1)
	return entry, ok, nil
}

func openCodeMessageEntry(source string, record map[string]any, rowID, rowSessionID string, sourceLine int) (usage.Entry, bool) {
	if record == nil {
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
	}
	tokens = applyTotalFallback(tokens, uintField(tokenBlock, "total"))
	if !nonZero(tokens) {
		return usage.Entry{}, false
	}
	model := stringField(record, "modelID")
	if model == "" {
		return usage.Entry{}, false
	}
	timestamp := time.Unix(0, 0).UTC()
	if parsed, ok := parseTimestamp(objectAt(record["time"])["created"]); ok {
		timestamp = parsed
	}
	sessionID := firstNonEmpty(rowSessionID, stringField(record, "sessionID"), "unknown")
	messageID := firstNonEmpty(rowID, stringField(record, "id"))
	entry := baseEntry(usage.ProviderOpenCode, timestamp, "opencode", "OpenCode", sessionID, model, "OpenCode", tokens)
	setSource(&entry, source, sourceLine, 0, 0)
	entry.ID = stableOpenCodeMessageID(messageID)
	if entry.ID == "" {
		entry.ID = stableEntryID(entry)
	}
	return entry, true
}

func stableOpenCodeMessageID(messageID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	return usage.StableID("opencode", strings.TrimSpace(messageID))
}
