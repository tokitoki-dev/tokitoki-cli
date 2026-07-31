package amp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if filepath.Ext(path) == ".json" {
				files = append(files, path)
			}
			continue
		}
		files = append(files, shared.CollectExt(filepath.Join(path, "threads"), ".json")...)
		if filepath.Base(path) == "threads" {
			files = append(files, shared.CollectExt(path, ".json")...)
		}
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseThreadFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	shared.SortEntries(entries)
	return entries, nil
}

func parseThreadFile(path string) ([]usage.Entry, error) {
	thread, err := shared.ReadJSONObject(path)
	if err != nil || thread == nil {
		return nil, err
	}
	threadID := shared.StringField(thread, "id")
	if threadID == "" {
		return nil, nil
	}
	messages := shared.ArrayAt(thread["messages"])
	if ledger := shared.ObjectAt(thread["usageLedger"]); ledger != nil {
		if events := shared.ArrayAt(ledger["events"]); len(events) > 0 {
			return ledgerEntries(path, threadID, messages, events), nil
		}
	}
	return messageEntries(path, threadID, messages), nil
}

func ledgerEntries(path, threadID string, messages []any, events []any) []usage.Entry {
	cacheTokens := cacheTokens(messages)
	entries := make([]usage.Entry, 0)
	for index, raw := range events {
		event := shared.ObjectAt(raw)
		if event == nil {
			continue
		}
		timestamp, ok := shared.ParseTimestamp(event["timestamp"])
		if !ok {
			continue
		}
		model := shared.StringField(event, "model")
		if model == "" {
			continue
		}
		tokenBlock := shared.ObjectAt(event["tokens"])
		if tokenBlock == nil {
			continue
		}
		cache := cacheTokens[int64Value(event["toMessageId"])]
		tokens := usage.TokenUsage{
			InputTokens:              shared.UintField(tokenBlock, "input"),
			OutputTokens:             shared.UintField(tokenBlock, "output"),
			CacheCreationInputTokens: cache.cacheCreation,
			CacheReadInputTokens:     cache.cacheRead,
		}
		tokens = shared.ApplyTotalFallback(tokens, shared.UintField(tokenBlock, "total"))
		if !shared.NonZero(tokens) {
			continue
		}
		messageID := shared.StringValue(event["id"])
		entry := shared.BaseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		shared.SetSource(&entry, path, index+1, 0, 0)
		entry.ID = shared.StableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries
}

func messageEntries(path, threadID string, messages []any) []usage.Entry {
	entries := make([]usage.Entry, 0)
	for index, raw := range messages {
		message := shared.ObjectAt(raw)
		if message == nil || shared.StringValue(message["role"]) != "assistant" {
			continue
		}
		usageBlock := shared.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := shared.ParseTimestamp(usageBlock["timestamp"])
		if !ok {
			timestamp, ok = shared.ParseTimestamp(message["timestamp"])
		}
		if !ok {
			continue
		}
		model := shared.StringField(usageBlock, "model")
		if model == "" {
			model = shared.StringValue(message["model"])
		}
		if model == "" {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              shared.UintField(usageBlock, "inputTokens"),
			OutputTokens:             shared.UintField(usageBlock, "outputTokens"),
			CacheCreationInputTokens: shared.UintField(usageBlock, "cacheCreationInputTokens"),
			CacheReadInputTokens:     shared.UintField(usageBlock, "cacheReadInputTokens"),
		}
		tokens = shared.ApplyTotalFallback(tokens, shared.UintField(usageBlock, "totalTokens"))
		if !shared.NonZero(tokens) {
			continue
		}
		messageID := shared.StringValue(message["messageId"])
		entry := shared.BaseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		shared.SetSource(&entry, path, index+1, 0, 0)
		entry.ID = shared.StableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries
}

type cache struct {
	cacheCreation uint64
	cacheRead     uint64
}

func cacheTokens(messages []any) map[int64]cache {
	tokens := make(map[int64]cache)
	for _, raw := range messages {
		message := shared.ObjectAt(raw)
		if message == nil || shared.StringValue(message["role"]) != "assistant" {
			continue
		}
		id := int64Value(message["messageId"])
		if id == 0 {
			continue
		}
		usageBlock := shared.ObjectAt(message["usage"])
		tokens[id] = cache{
			cacheCreation: shared.UintField(usageBlock, "cacheCreationInputTokens"),
			cacheRead:     shared.UintField(usageBlock, "cacheReadInputTokens"),
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
