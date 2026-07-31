package gemini

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, agentdata.CollectExt(root, ".json")...)
		files = append(files, agentdata.CollectExt(root, ".jsonl")...)
	}
	sort.Strings(files)
	files = agentdata.FilterFiles(agentdata.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		var fileEntries []usage.Entry
		var err error
		if strings.EqualFold(filepath.Ext(file), ".jsonl") {
			fileEntries, err = parseJSONLFile(file)
		} else {
			fileEntries, err = parseJSONFile(file)
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

type tokens struct {
	input    uint64
	output   uint64
	cached   uint64
	thoughts uint64
	tool     uint64
	total    uint64
	hasTotal bool
}

func parseJSONFile(path string) ([]usage.Entry, error) {
	record, err := agentdata.ReadJSONObject(path)
	if err != nil || record == nil {
		return nil, err
	}
	fallback := agentdata.FileModifiedTime(path)
	sessionID := agentdata.FirstStringField(record, "sessionId", "session_id")
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	sessionTimestamp := firstTimestamp(record, fallback, "startTime", "lastUpdated", "timestamp", "created_at")
	if messages := agentdata.ArrayAt(record["messages"]); len(messages) > 0 {
		entries := make([]usage.Entry, 0)
		for index, raw := range messages {
			message := agentdata.ObjectAt(raw)
			if agentdata.StringField(message, "type") != "gemini" {
				continue
			}
			if entry, ok := directEntry(message, path, index+1, "", sessionID, sessionTimestamp); ok {
				entries = append(entries, entry)
			}
		}
		return entries, nil
	}
	if agentdata.StringField(record, "type") == "gemini" {
		if entry, ok := directEntry(record, path, 1, "", sessionID, fallback); ok {
			return []usage.Entry{entry}, nil
		}
		return nil, nil
	}
	return statsEntries(recordStats(record), path, 1, agentdata.StringField(record, "model"), sessionID, sessionTimestamp), nil
}

func parseJSONLFile(path string) ([]usage.Entry, error) {
	lines, err := agentdata.ReadJSONLines(path)
	if err != nil {
		return nil, err
	}
	fallback := agentdata.FileModifiedTime(path)
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	currentModel := ""
	entries := make([]usage.Entry, 0)
	indexByMessageID := make(map[string]int)
	for _, line := range lines {
		record := line.Value
		if value := agentdata.FirstStringField(record, "sessionId", "session_id"); value != "" {
			sessionID = value
		}
		if model := agentdata.StringField(record, "model"); model != "" {
			currentModel = model
		}
		if agentdata.StringField(record, "type") == "gemini" {
			entry, ok := directEntry(record, path, line.Line, currentModel, sessionID, fallback)
			if !ok {
				continue
			}
			messageID := agentdata.StringField(record, "id")
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
			entries = append(entries, statsEntries(stats, path, line.Line, currentModel, sessionID, timestamp)...)
		}
	}
	return entries, nil
}

func directEntry(record map[string]any, path string, line int, modelHint, sessionID string, fallback time.Time) (usage.Entry, bool) {
	tokens, ok := parseTokens(record["tokens"])
	if !ok {
		return usage.Entry{}, false
	}
	model := agentdata.StringField(record, "model")
	if model == "" {
		model = modelHint
	}
	timestamp := firstTimestamp(record, fallback, "timestamp", "created_at")
	return buildGeminiEntry(path, line, model, sessionID, timestamp, tokens, true, agentdata.StringField(record, "id"))
}

func statsEntries(stats map[string]any, path string, line int, modelHint, sessionID string, timestamp time.Time) []usage.Entry {
	if stats == nil {
		return nil
	}
	if models := agentdata.ObjectAt(stats["models"]); models != nil {
		entries := make([]usage.Entry, 0)
		for model, raw := range models {
			data := agentdata.ObjectAt(raw)
			tokens, ok := parseTokens(data["tokens"])
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
	tokens, ok := parseTokens(stats)
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

func buildGeminiEntry(path string, line int, model, sessionID string, timestamp time.Time, tokens tokens, direct bool, messageID string) (usage.Entry, bool) {
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
		tokenUsage = usageprovider.ApplyTotalFallback(tokenUsage, tokens.total)
	} else if tokenUsage.TotalTokens == 0 {
		tokenUsage.TotalTokens = usageprovider.TotalUsage(tokenUsage)
	}
	if !usageprovider.NonZero(tokenUsage) {
		return usage.Entry{}, false
	}
	entry := usageprovider.BaseEntry(usage.ProviderGemini, timestamp, "gemini", "Gemini", sessionID, model, "Gemini CLI", tokenUsage)
	usageprovider.SetSource(&entry, path, line, 0, 0)
	entry.ID = usageprovider.StableEntryID(entry, messageID)
	return entry, true
}

func parseTokens(raw any) (tokens, bool) {
	record := agentdata.ObjectAt(raw)
	if record == nil {
		return tokens{}, false
	}
	tokens := tokens{
		input:    agentdata.UintField(record, "input", "prompt", "input_tokens", "prompt_tokens"),
		output:   agentdata.UintField(record, "output", "candidates", "output_tokens", "candidates_tokens"),
		cached:   agentdata.UintField(record, "cached", "cached_tokens"),
		thoughts: agentdata.UintField(record, "thoughts", "reasoning", "thoughts_tokens", "reasoning_tokens"),
		tool:     agentdata.UintField(record, "tool", "tool_tokens"),
		total:    agentdata.UintField(record, "total", "total_tokens"),
	}
	tokens.hasTotal = tokens.total > 0
	return tokens, true
}

func normalizeGeminiInput(tokens tokens, direct bool) (uint64, uint64) {
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
	if stats := agentdata.ObjectAt(record["stats"]); stats != nil {
		return stats
	}
	result := agentdata.ObjectAt(record["result"])
	return agentdata.ObjectAt(result["stats"])
}

func firstTimestamp(record map[string]any, fallback time.Time, keys ...string) time.Time {
	for _, key := range keys {
		if timestamp, ok := agentdata.ParseTimestamp(record[key]); ok {
			return timestamp
		}
	}
	return fallback
}
