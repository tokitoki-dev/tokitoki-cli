package agentusage

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadGeminiEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectExt(root, ".json")...)
		files = append(files, collectExt(root, ".jsonl")...)
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		var fileEntries []usage.Entry
		var err error
		if strings.EqualFold(filepath.Ext(file), ".jsonl") {
			fileEntries, err = parseGeminiJSONLFile(file)
		} else {
			fileEntries, err = parseGeminiJSONFile(file)
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

type geminiTokens struct {
	input    uint64
	output   uint64
	cached   uint64
	thoughts uint64
	tool     uint64
	total    uint64
	hasTotal bool
}

func parseGeminiJSONFile(path string) ([]usage.Entry, error) {
	record, err := readJSONObject(path)
	if err != nil || record == nil {
		return nil, err
	}
	fallback := fileModifiedTime(path)
	sessionID := firstStringField(record, "sessionId", "session_id")
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	sessionTimestamp := firstTimestamp(record, fallback, "startTime", "lastUpdated", "timestamp", "created_at")
	if messages := arrayAt(record["messages"]); len(messages) > 0 {
		entries := make([]usage.Entry, 0)
		for index, raw := range messages {
			message := objectAt(raw)
			if stringField(message, "type") != "gemini" {
				continue
			}
			if entry, ok := geminiDirectEntry(message, path, index+1, "", sessionID, sessionTimestamp); ok {
				entries = append(entries, entry)
			}
		}
		return entries, nil
	}
	if stringField(record, "type") == "gemini" {
		if entry, ok := geminiDirectEntry(record, path, 1, "", sessionID, fallback); ok {
			return []usage.Entry{entry}, nil
		}
		return nil, nil
	}
	return geminiStatsEntries(recordStats(record), path, 1, stringField(record, "model"), sessionID, sessionTimestamp), nil
}

func parseGeminiJSONLFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path)
	if err != nil {
		return nil, err
	}
	fallback := fileModifiedTime(path)
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	currentModel := ""
	entries := make([]usage.Entry, 0)
	indexByMessageID := make(map[string]int)
	for _, line := range lines {
		record := line.value
		if value := firstStringField(record, "sessionId", "session_id"); value != "" {
			sessionID = value
		}
		if model := stringField(record, "model"); model != "" {
			currentModel = model
		}
		if stringField(record, "type") == "gemini" {
			entry, ok := geminiDirectEntry(record, path, line.line, currentModel, sessionID, fallback)
			if !ok {
				continue
			}
			messageID := stringField(record, "id")
			if messageID != "" {
				if index, exists := indexByMessageID[messageID]; exists {
					entries[index] = entry
					continue
				}
				indexByMessageID[messageID] = len(entries)
			}
			entries = append(entries, entry)
			continue
		}
		if stats := recordStats(record); stats != nil {
			timestamp := firstTimestamp(record, fallback, "timestamp")
			entries = append(entries, geminiStatsEntries(stats, path, line.line, currentModel, sessionID, timestamp)...)
		}
	}
	return entries, nil
}

func geminiDirectEntry(record map[string]any, path string, line int, modelHint, sessionID string, fallback time.Time) (usage.Entry, bool) {
	tokens, ok := parseGeminiTokens(record["tokens"])
	if !ok {
		return usage.Entry{}, false
	}
	model := stringField(record, "model")
	if model == "" {
		model = modelHint
	}
	timestamp := firstTimestamp(record, fallback, "timestamp", "created_at")
	return buildGeminiEntry(path, line, model, sessionID, timestamp, tokens, true, stringField(record, "id"))
}

func geminiStatsEntries(stats map[string]any, path string, line int, modelHint, sessionID string, timestamp time.Time) []usage.Entry {
	if stats == nil {
		return nil
	}
	if models := objectAt(stats["models"]); models != nil {
		entries := make([]usage.Entry, 0)
		for model, raw := range models {
			data := objectAt(raw)
			tokens, ok := parseGeminiTokens(data["tokens"])
			if !ok {
				continue
			}
			if entry, ok := buildGeminiEntry(path, line, model, sessionID, timestamp, tokens, false, ""); ok {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 {
			return entries
		}
	}
	tokens, ok := parseGeminiTokens(stats)
	if !ok {
		return nil
	}
	model := modelHint
	if model == "" {
		model = "unknown"
	}
	entry, ok := buildGeminiEntry(path, line, model, sessionID, timestamp, tokens, false, "")
	if !ok {
		return nil
	}
	return []usage.Entry{entry}
}

func buildGeminiEntry(path string, line int, model, sessionID string, timestamp time.Time, tokens geminiTokens, direct bool, messageID string) (usage.Entry, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return usage.Entry{}, false
	}
	input, cacheRead := normalizeGeminiInput(tokens, direct)
	tokenUsage := usage.TokenUsage{
		InputTokens:           input + tokens.tool,
		OutputTokens:          tokens.output,
		CacheReadInputTokens:  cacheRead,
		ReasoningOutputTokens: tokens.thoughts,
	}
	if tokens.hasTotal {
		tokenUsage = applyTotalFallback(tokenUsage, tokens.total)
	} else if tokenUsage.TotalTokens == 0 {
		tokenUsage.TotalTokens = totalUsage(tokenUsage)
	}
	if !nonZero(tokenUsage) {
		return usage.Entry{}, false
	}
	entry := baseEntry(usage.ProviderGemini, timestamp, "gemini", "Gemini", sessionID, model, "Gemini CLI", tokenUsage)
	setSource(&entry, path, line, 0, 0)
	entry.ID = stableEntryID(entry, messageID)
	return entry, true
}

func parseGeminiTokens(raw any) (geminiTokens, bool) {
	record := objectAt(raw)
	if record == nil {
		return geminiTokens{}, false
	}
	tokens := geminiTokens{
		input:    uintField(record, "input", "prompt", "input_tokens", "prompt_tokens"),
		output:   uintField(record, "output", "candidates", "output_tokens", "candidates_tokens"),
		cached:   uintField(record, "cached", "cached_tokens"),
		thoughts: uintField(record, "thoughts", "reasoning", "thoughts_tokens", "reasoning_tokens"),
		tool:     uintField(record, "tool", "tool_tokens"),
		total:    uintField(record, "total", "total_tokens"),
	}
	tokens.hasTotal = tokens.total > 0
	return tokens, true
}

func normalizeGeminiInput(tokens geminiTokens, direct bool) (uint64, uint64) {
	if !direct {
		cachedPortion := tokens.input
		if tokens.cached < cachedPortion {
			cachedPortion = tokens.cached
		}
		return tokens.input - cachedPortion, tokens.cached
	}
	inclusiveTotal := tokens.input + tokens.output + tokens.thoughts + tokens.tool
	exclusiveTotal := inclusiveTotal + tokens.cached
	if tokens.cached > 0 && tokens.hasTotal && tokens.total == inclusiveTotal && tokens.total != exclusiveTotal {
		cachedPortion := tokens.input
		if tokens.cached < cachedPortion {
			cachedPortion = tokens.cached
		}
		return tokens.input - cachedPortion, tokens.cached
	}
	return tokens.input, tokens.cached
}

func recordStats(record map[string]any) map[string]any {
	if stats := objectAt(record["stats"]); stats != nil {
		return stats
	}
	result := objectAt(record["result"])
	return objectAt(result["stats"])
}

func firstTimestamp(record map[string]any, fallback time.Time, keys ...string) time.Time {
	for _, key := range keys {
		if timestamp, ok := parseTimestamp(record[key]); ok {
			return timestamp
		}
	}
	return fallback
}
