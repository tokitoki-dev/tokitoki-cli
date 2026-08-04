package kimi

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const defaultModel = "kimi-for-coding"

func wireFiles(paths []string) []string {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, agentdata.CollectFiles(filepath.Join(root, "sessions"), isWireFile)...)
	}
	sort.Strings(files)
	return agentdata.UniqueStrings(files)
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := agentdata.FilterFiles(wireFiles(paths), filter)

	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		fileEntries, _, err := parseWireFileFrom(file, 0)
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
	usageprovider.SortEntries(entries)
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

func parseWireFileFrom(path string, start int64) ([]usage.Entry, int64, error) {
	lines, consumed, err := agentdata.ReadJSONLinesFrom(path, start, `usage`)
	if err != nil {
		return nil, 0, err
	}
	configModel := configModel(path)
	sessionID := sessionID(path)
	fallback := agentdata.FileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		var record record
		var ok bool
		if agentdata.StringField(line.Value, "type") == "usage.record" {
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
		entry := usageprovider.BaseEntry(usage.ProviderKimi, timestamp, "kimi", "Kimi", sessionID, model, "Kimi", record.tokens)
		usageprovider.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = usageprovider.StableEntryID(entry, record.messageID)
		entries = append(entries, entry)
	}
	return entries, consumed, nil
}

func parseStatusUpdate(value map[string]any) (record, bool) {
	message := agentdata.ObjectAt(value["message"])
	if agentdata.StringField(message, "type") != "StatusUpdate" {
		return record{}, false
	}
	payload := agentdata.ObjectAt(message["payload"])
	tokenUsage := agentdata.ObjectAt(payload["token_usage"])
	if tokenUsage == nil {
		return record{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              agentdata.UintField(tokenUsage, "input_other"),
		OutputTokens:             agentdata.UintField(tokenUsage, "output"),
		CacheCreationInputTokens: agentdata.UintField(tokenUsage, "input_cache_creation"),
		CacheReadInputTokens:     agentdata.UintField(tokenUsage, "input_cache_read"),
	}
	tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(tokenUsage, "total"))
	if !usageprovider.NonZero(tokens) {
		return record{}, false
	}
	record := record{tokens: tokens, messageID: agentdata.StringField(payload, "message_id")}
	record.timestamp, record.hasTime = agentdata.ParseTimestamp(value["timestamp"])
	return record, true
}

func parseUsageRecord(value map[string]any) (record, bool) {
	// Session-scoped records are cumulative totals; only turn records count.
	if agentdata.StringField(value, "usageScope") != "turn" {
		return record{}, false
	}
	tokenUsage := agentdata.ObjectAt(value["usage"])
	if tokenUsage == nil {
		return record{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              agentdata.UintField(tokenUsage, "inputOther"),
		OutputTokens:             agentdata.UintField(tokenUsage, "output"),
		CacheCreationInputTokens: agentdata.UintField(tokenUsage, "inputCacheCreation"),
		CacheReadInputTokens:     agentdata.UintField(tokenUsage, "inputCacheRead"),
	}
	tokens = usageprovider.ApplyTotalFallback(tokens, 0)
	if !usageprovider.NonZero(tokens) {
		return record{}, false
	}
	record := record{
		tokens: tokens,
		model:  strings.TrimPrefix(agentdata.StringField(value, "model"), "kimi-code/"),
	}
	record.timestamp, record.hasTime = agentdata.ParseTimestamp(value["time"])
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
	config, err := agentdata.ReadJSONObject(filepath.Join(root, "config.json"))
	if err != nil || config == nil {
		return defaultModel
	}
	if model := agentdata.StringField(config, "model"); model != "" {
		return model
	}
	return defaultModel
}
