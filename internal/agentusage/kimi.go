package agentusage

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const kimiDefaultModel = "kimi-for-coding"

func loadKimiEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectFiles(filepath.Join(root, "sessions"), isKimiWireFile)...)
	}
	sort.Strings(files)
	files = filterFiles(uniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		fileEntries, err := parseKimiWireFile(file)
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
	sortEntries(entries)
	return entries, nil
}

func isKimiWireFile(path string) bool {
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

// kimiRecord is one usable wire line, normalized across the old
// StatusUpdate format and the new Kimi Code usage.record format.
type kimiRecord struct {
	tokens    usage.TokenUsage
	timestamp time.Time
	hasTime   bool
	model     string // empty means fall back to the config.json model
	messageID string
}

func parseKimiWireFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path, `usage`)
	if err != nil {
		return nil, err
	}
	configModel := kimiConfigModel(path)
	sessionID := kimiSessionID(path)
	fallback := fileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		var record kimiRecord
		var ok bool
		if stringField(line.value, "type") == "usage.record" {
			record, ok = parseKimiUsageRecord(line.value)
		} else {
			record, ok = parseKimiStatusUpdate(line.value)
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
		entry := baseEntry(usage.ProviderKimi, timestamp, "kimi", "Kimi", sessionID, model, "Kimi", record.tokens)
		setSource(&entry, path, line.line, line.start, line.end)
		entry.ID = stableEntryID(entry, record.messageID)
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseKimiStatusUpdate(value map[string]any) (kimiRecord, bool) {
	message := objectAt(value["message"])
	if stringField(message, "type") != "StatusUpdate" {
		return kimiRecord{}, false
	}
	payload := objectAt(message["payload"])
	tokenUsage := objectAt(payload["token_usage"])
	if tokenUsage == nil {
		return kimiRecord{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              uintField(tokenUsage, "input_other"),
		OutputTokens:             uintField(tokenUsage, "output"),
		CacheCreationInputTokens: uintField(tokenUsage, "input_cache_creation"),
		CacheReadInputTokens:     uintField(tokenUsage, "input_cache_read"),
	}
	tokens = applyTotalFallback(tokens, uintField(tokenUsage, "total"))
	if !nonZero(tokens) {
		return kimiRecord{}, false
	}
	record := kimiRecord{tokens: tokens, messageID: stringField(payload, "message_id")}
	record.timestamp, record.hasTime = parseTimestamp(value["timestamp"])
	return record, true
}

func parseKimiUsageRecord(value map[string]any) (kimiRecord, bool) {
	// Session-scoped records are cumulative totals; only turn records count.
	if stringField(value, "usageScope") != "turn" {
		return kimiRecord{}, false
	}
	tokenUsage := objectAt(value["usage"])
	if tokenUsage == nil {
		return kimiRecord{}, false
	}
	tokens := usage.TokenUsage{
		InputTokens:              uintField(tokenUsage, "inputOther"),
		OutputTokens:             uintField(tokenUsage, "output"),
		CacheCreationInputTokens: uintField(tokenUsage, "inputCacheCreation"),
		CacheReadInputTokens:     uintField(tokenUsage, "inputCacheRead"),
	}
	tokens = applyTotalFallback(tokens, 0)
	if !nonZero(tokens) {
		return kimiRecord{}, false
	}
	record := kimiRecord{
		tokens: tokens,
		model:  strings.TrimPrefix(stringField(value, "model"), "kimi-code/"),
	}
	record.timestamp, record.hasTime = parseTimestamp(value["time"])
	return record, true
}

// kimiSessionID returns the session directory name for either layout.
func kimiSessionID(path string) string {
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

func kimiConfigModel(path string) string {
	root := kimiRoot(path)
	if root == "" {
		return kimiDefaultModel
	}
	config, err := readJSONObject(filepath.Join(root, "config.json"))
	if err != nil || config == nil {
		return kimiDefaultModel
	}
	if model := stringField(config, "model"); model != "" {
		return model
	}
	return kimiDefaultModel
}
