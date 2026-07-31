package kimi

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const defaultModel = "kimi-for-coding"

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, shared.CollectFiles(filepath.Join(root, "sessions"), isWireFile)...)
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		fileEntries, err := parseWireFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			entries = append(entries, entry)
		}
	}
	shared.SortEntries(entries)
	return entries, nil
}

func isWireFile(path string) bool {
	if filepath.Base(path) != "wire.jsonl" {
		return false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := range parts {
		if parts[i] != "sessions" {
			continue
		}
		// Old layout: sessions/<group>/<session>/wire.jsonl
		// New layout: sessions/<workspace>/<session>/agents/<agent>/wire.jsonl
		if i+3 == len(parts)-1 || i+5 == len(parts)-1 {
			return true
		}
	}
	return false
}

// record is one usable wire line, normalized across the old
// StatusUpdate format and the new Kimi Code usage.record format.
type record struct {
	tokens    usage.TokenUsage
	timestamp time.Time
	hasTime   bool
	model     string // empty means fall back to the config.json model
	messageID string
}

func parseWireFile(path string) ([]usage.Entry, error) {
	lines, err := shared.ReadJSONLines(path, `usage`)
	if err != nil {
		return nil, err
	}
	configModel := configModel(path)
	sessionID := sessionID(path)
	fallback := shared.FileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		var record record
		var ok bool
		if shared.StringField(line.Value, "type") == "usage.record" {
			record, ok = parseUsageRecord(line.Value)
		} else {
			record, ok = parseStatusUpdate(line.Value)
		}
		if !ok {
			continue
		}
		timestamp := record.timestamp
		if !record.hasTime {
			timestamp = fallback
		}
		model := record.model
		if model == "" {
			model = configModel
		}
		entry := shared.BaseEntry(usage.ProviderKimi, timestamp, "kimi", "Kimi", sessionID, model, "Kimi", record.tokens)
		shared.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = shared.StableEntryID(entry, record.messageID)
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseStatusUpdate(value map[string]any) (record, bool) {
	message := shared.ObjectAt(value["message"])
	if shared.StringField(message, "type") != "StatusUpdate" {
		return record{}, false
	}
	payload := shared.ObjectAt(message["payload"])
	tokenUsage := shared.ObjectAt(payload["token_usage"])
	if tokenUsage == nil {
		return record{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              shared.UintField(tokenUsage, "input_other"),
		OutputTokens:             shared.UintField(tokenUsage, "output"),
		CacheCreationInputTokens: shared.UintField(tokenUsage, "input_cache_creation"),
		CacheReadInputTokens:     shared.UintField(tokenUsage, "input_cache_read"),
	}
	tokens = shared.ApplyTotalFallback(tokens, shared.UintField(tokenUsage, "total"))
	if !shared.NonZero(tokens) {
		return record{}, false
	}
	record := record{tokens: tokens, messageID: shared.StringField(payload, "message_id")}
	record.timestamp, record.hasTime = shared.ParseTimestamp(value["timestamp"])
	return record, true
}

func parseUsageRecord(value map[string]any) (record, bool) {
	// Session-scoped records are cumulative totals; only turn records count.
	if shared.StringField(value, "usageScope") != "turn" {
		return record{}, false
	}
	tokenUsage := shared.ObjectAt(value["usage"])
	if tokenUsage == nil {
		return record{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              shared.UintField(tokenUsage, "inputOther"),
		OutputTokens:             shared.UintField(tokenUsage, "output"),
		CacheCreationInputTokens: shared.UintField(tokenUsage, "inputCacheCreation"),
		CacheReadInputTokens:     shared.UintField(tokenUsage, "inputCacheRead"),
	}
	tokens = shared.ApplyTotalFallback(tokens, 0)
	if !shared.NonZero(tokens) {
		return record{}, false
	}
	record := record{
		tokens: tokens,
		model:  strings.TrimPrefix(shared.StringField(value, "model"), "kimi-code/"),
	}
	record.timestamp, record.hasTime = shared.ParseTimestamp(value["time"])
	return record, true
}

// sessionID returns the session directory name for either layout.
func sessionID(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(dir)) == "agents" {
		dir = filepath.Dir(filepath.Dir(dir))
	}
	sessionID := filepath.Base(dir)
	if sessionID == "" || sessionID == "." {
		return "unknown"
	}
	return sessionID
}

// kimiRoot walks up from a wire file to the directory containing "sessions",
// which is the Kimi data root regardless of layout depth.
func kimiRoot(path string) string {
	for dir := filepath.Dir(path); ; {
		parent := filepath.Dir(dir)
		if filepath.Base(dir) == "sessions" {
			return parent
		}
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func configModel(path string) string {
	root := kimiRoot(path)
	if root == "" {
		return defaultModel
	}
	config, err := shared.ReadJSONObject(filepath.Join(root, "config.json"))
	if err != nil || config == nil {
		return defaultModel
	}
	if model := shared.StringField(config, "model"); model != "" {
		return model
	}
	return defaultModel
}
