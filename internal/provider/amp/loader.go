package amp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

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
		files = append(files, agentdata.CollectExt(filepath.Join(path, "threads"), ".json")...)
		if filepath.Base(path) == "threads" {
			files = append(files, agentdata.CollectExt(path, ".json")...)
		}
	}
	sort.Strings(files)
	files = agentdata.FilterFiles(agentdata.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseThreadFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

func parseThreadFile(path string) ([]usage.Entry, error) {
	thread, err := agentdata.ReadJSONObject(path)
	if err != nil || thread == nil {
		return nil, err
	}
	threadID := agentdata.StringField(thread, "id")
	if threadID == "" {
		return nil, nil
	}
	messages := agentdata.ArrayAt(thread["messages"])
	if ledger := agentdata.ObjectAt(thread["usageLedger"]); ledger != nil {
		if events := agentdata.ArrayAt(ledger["events"]); len(events) > 0 {
			return ledgerEntries(path, threadID, messages, events), nil
		}
	}
	return messageEntries(path, threadID, messages), nil
}

func ledgerEntries(path, threadID string, messages []any, events []any) []usage.Entry {
	cacheTokens := cacheTokens(messages)
	entries := make([]usage.Entry, 0)
	for index, raw := range events {
		event := agentdata.ObjectAt(raw)
		if event == nil {
			continue
		}
		timestamp, ok := agentdata.ParseTimestamp(event["timestamp"])
		if !ok {
			continue
		}
		model := agentdata.StringField(event, "model")
		if model == "" {
			continue
		}
		tokenBlock := agentdata.ObjectAt(event["tokens"])
		if tokenBlock == nil {
			continue
		}
		cache := cacheTokens[int64Value(event["toMessageId"])]
		tokens := usage.TokenUsage{
			InputTokens:              agentdata.UintField(tokenBlock, "input"),
			OutputTokens:             agentdata.UintField(tokenBlock, "output"),
			CacheCreationInputTokens: cache.cacheCreation,
			CacheReadInputTokens:     cache.cacheRead,
		}
		tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(tokenBlock, "total"))
		if !usageprovider.NonZero(tokens) {
			continue
		}
		messageID := agentdata.StringValue(event["id"])
		entry := usageprovider.BaseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		usageprovider.SetSource(&entry, path, index+1, 0, 0)
		entry.ID = usageprovider.StableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries
}

func messageEntries(path, threadID string, messages []any) []usage.Entry {
	entries := make([]usage.Entry, 0)
	for index, raw := range messages {
		message := agentdata.ObjectAt(raw)
		if message == nil || agentdata.StringValue(message["role"]) != "assistant" {
			continue
		}
		usageBlock := agentdata.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := agentdata.ParseTimestamp(usageBlock["timestamp"])
		if !ok {
			timestamp, ok = agentdata.ParseTimestamp(message["timestamp"])
		}
		if !ok {
			continue
		}
		model := agentdata.StringField(usageBlock, "model")
		if model == "" {
			model = agentdata.StringValue(message["model"])
		}
		if model == "" {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              agentdata.UintField(usageBlock, "inputTokens"),
			OutputTokens:             agentdata.UintField(usageBlock, "outputTokens"),
			CacheCreationInputTokens: agentdata.UintField(usageBlock, "cacheCreationInputTokens"),
			CacheReadInputTokens:     agentdata.UintField(usageBlock, "cacheReadInputTokens"),
		}
		tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(usageBlock, "totalTokens"))
		if !usageprovider.NonZero(tokens) {
			continue
		}
		messageID := agentdata.StringValue(message["messageId"])
		entry := usageprovider.BaseEntry(usage.ProviderAmp, timestamp, "amp", "Amp", threadID, model, "Amp", tokens)
		usageprovider.SetSource(&entry, path, index+1, 0, 0)
		entry.ID = usageprovider.StableEntryID(entry, messageID)
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
		message := agentdata.ObjectAt(raw)
		if message == nil || agentdata.StringValue(message["role"]) != "assistant" {
			continue
		}
		id := int64Value(message["messageId"])
		if id == 0 {
			continue
		}
		usageBlock := agentdata.ObjectAt(message["usage"])
		tokens[id] = cache{
			cacheCreation: agentdata.UintField(usageBlock, "cacheCreationInputTokens"),
			cacheRead:     agentdata.UintField(usageBlock, "cacheReadInputTokens"),
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
