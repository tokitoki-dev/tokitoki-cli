package agentusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadAmpEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if filepath.Ext(path) == ".json" {
				files = append(files, path)
			}
			continue
		}
		files = append(files, collectExt(filepath.Join(path, "threads"), ".json")...)
		if filepath.Base(path) == "threads" {
			files = append(files, collectExt(path, ".json")...)
		}
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseAmpThreadFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func parseAmpThreadFile(path string) ([]usage.Entry, error) {
	thread, err := readJSONObject(path)
	if err != nil || thread == nil {
		return nil, err
	}
	threadID := stringField(thread, "id")
	if threadID == "" {
		return nil, nil
	}
	messages := arrayAt(thread["messages"])
	if ledger := objectAt(thread["usageLedger"]); ledger != nil {
		if events := arrayAt(ledger["events"]); len(events) > 0 {
			return ampLedgerEntries(path, threadID, messages, events), nil
		}
	}
	return ampMessageEntries(path, threadID, messages), nil
}

func ampLedgerEntries(path, threadID string, messages []any, events []any) []usage.Entry {
	cacheTokens := ampCacheTokens(messages)
	entries := make([]usage.Entry, 0)
	for index, raw := range events {
		event := objectAt(raw)
		if event == nil {
			continue
		}
		timestamp, ok := parseTimestamp(event["timestamp"])
		if !ok {
			continue
		}
		model := stringField(event, "model")
		if model == "" {
			continue
		}
		tokenBlock := objectAt(event["tokens"])
		if tokenBlock == nil {
			continue
		}
		cache := cacheTokens[int64Value(event["toMessageId"])]
		tokens := usage.TokenUsage{
			InputTokens:              uintField(tokenBlock, "input"),
			OutputTokens:             uintField(tokenBlock, "output"),
			CacheCreationInputTokens: cache.cacheCreation,
			CacheReadInputTokens:     cache.cacheRead,
		}
		tokens = applyTotalFallback(tokens, uintField(tokenBlock, "total"))
		if !nonZero(tokens) {
			continue
		}
		messageID := stringValue(event["id"])
		entry := baseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		setSource(&entry, path, index+1, 0, 0)
		entry.ID = stableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries
}

func ampMessageEntries(path, threadID string, messages []any) []usage.Entry {
	entries := make([]usage.Entry, 0)
	for index, raw := range messages {
		message := objectAt(raw)
		if message == nil || stringValue(message["role"]) != "assistant" {
			continue
		}
		usageBlock := objectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := parseTimestamp(usageBlock["timestamp"])
		if !ok {
			timestamp, ok = parseTimestamp(message["timestamp"])
		}
		if !ok {
			continue
		}
		model := stringField(usageBlock, "model")
		if model == "" {
			model = stringValue(message["model"])
		}
		if model == "" {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              uintField(usageBlock, "inputTokens"),
			OutputTokens:             uintField(usageBlock, "outputTokens"),
			CacheCreationInputTokens: uintField(usageBlock, "cacheCreationInputTokens"),
			CacheReadInputTokens:     uintField(usageBlock, "cacheReadInputTokens"),
		}
		tokens = applyTotalFallback(tokens, uintField(usageBlock, "totalTokens"))
		if !nonZero(tokens) {
			continue
		}
		messageID := stringValue(message["messageId"])
		entry := baseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		setSource(&entry, path, index+1, 0, 0)
		entry.ID = stableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries
}

type ampCache struct {
	cacheCreation uint64
	cacheRead     uint64
}

func ampCacheTokens(messages []any) map[int64]ampCache {
	tokens := make(map[int64]ampCache)
	for _, raw := range messages {
		message := objectAt(raw)
		if message == nil || stringValue(message["role"]) != "assistant" {
			continue
		}
		id := int64Value(message["messageId"])
		if id == 0 {
			continue
		}
		usageBlock := objectAt(message["usage"])
		tokens[id] = ampCache{
			cacheCreation: uintField(usageBlock, "cacheCreationInputTokens"),
			cacheRead:     uintField(usageBlock, "cacheReadInputTokens"),
		}
	}
	return tokens
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := strconv.ParseInt(typed.String(), 10, 64)
		return parsed
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}
