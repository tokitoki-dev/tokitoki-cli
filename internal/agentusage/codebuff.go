package agentusage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
)

type codebuffUsage struct {
	model                    string
	inputTokens              uint64
	outputTokens             uint64
	cacheCreationInputTokens uint64
	cacheReadInputTokens     uint64
	extraTotalTokens         uint64
}

func loadCodebuffEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectCodebuffChatFiles(root)...)
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entriesByID := make(map[string]usage.Entry)
	for _, file := range files {
		fileEntries, err := parseCodebuffChatFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			entriesByID[entry.ID] = entry
		}
	}
	entries := make([]usage.Entry, 0, len(entriesByID))
	for _, entry := range entriesByID {
		entries = append(entries, entry)
	}
	sortEntries(entries)
	return entries, nil
}

func collectCodebuffChatFiles(root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if filepath.Base(root) == "chat-messages.json" {
			return []string{root}
		}
		return nil
	}
	projectRoot := root
	if filepath.Base(root) != "projects" {
		projectRoot = filepath.Join(root, "projects")
	}
	return collectFiles(projectRoot, func(path string) bool {
		return filepath.Base(path) == "chat-messages.json"
	})
}

func parseCodebuffChatFile(path string) ([]usage.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var messages []any
	if err := decoder.Decode(&messages); err != nil {
		return nil, nil
	}
	sessionID, chatID := codebuffSessionContext(path)
	chatTimestamp, hasChatTimestamp := parseCodebuffChatTimestamp(chatID)
	fileTimestamp := fileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for index, raw := range messages {
		message := objectAt(raw)
		if !isCodebuffAssistant(message) {
			continue
		}
		parsedUsage := extractCodebuffUsage(message)
		tokens := usage.TokenUsage{
			InputTokens:              parsedUsage.inputTokens,
			OutputTokens:             parsedUsage.outputTokens,
			CacheCreationInputTokens: parsedUsage.cacheCreationInputTokens,
			CacheReadInputTokens:     parsedUsage.cacheReadInputTokens,
			ReasoningOutputTokens:    parsedUsage.extraTotalTokens,
		}
		if tokens.TotalTokens == 0 {
			tokens.TotalTokens = totalUsage(tokens)
		}
		if !nonZero(tokens) {
			continue
		}
		model := parsedUsage.model
		if model == "" {
			model = "codebuff-unknown"
		}
		timestamp, ok := codebuffMessageTimestamp(message)
		if !ok && hasChatTimestamp {
			timestamp = chatTimestamp
			ok = true
		}
		if !ok {
			timestamp = fileTimestamp
		}
		entry := baseEntry(usage.ProviderCodebuff, timestamp, "codebuff", "Codebuff", sessionID, model, "Codebuff", tokens)
		setSource(&entry, path, index+1, 0, 0)
		entry.ID = stableEntryID(entry, codebuffDedupKey(message, sessionID, timestamp, model, tokens, index))
		entries = append(entries, entry)
	}
	return entries, nil
}

func codebuffSessionContext(path string) (string, string) {
	chatID := filepath.Base(filepath.Dir(path))
	chatsDir := filepath.Dir(filepath.Dir(path))
	projectDir := filepath.Dir(chatsDir)
	project := filepath.Base(projectDir)
	channel := filepath.Base(filepath.Dir(filepath.Dir(projectDir)))
	if channel == "." || channel == string(filepath.Separator) || channel == "" {
		channel = "manicode"
	}
	if project == "." || project == string(filepath.Separator) || project == "" {
		project = "unknown"
	}
	if chatID == "." || chatID == string(filepath.Separator) || chatID == "" {
		chatID = "unknown"
	}
	return channel + "/" + project + "/" + chatID, chatID
}

func isCodebuffAssistant(message map[string]any) bool {
	role := firstStringField(message, "variant", "role")
	return role == "ai" || role == "agent" || role == "assistant"
}

func extractCodebuffUsage(message map[string]any) codebuffUsage {
	var usage codebuffUsage
	metadata := objectAt(message["metadata"])
	if metadata != nil {
		usage.model = stringField(metadata, "model")
		mergeCodebuffUsage(&usage, parseCodebuffUsageObject(metadata["usage"]))
		mergeCodebuffUsage(&usage, parseCodebuffUsageObject(objectAt(metadata["codebuff"])["usage"]))
		if runState := codebuffRunStateUsage(metadata); runState != nil {
			mergeCodebuffUsage(&usage, *runState)
		}
	}
	return usage
}

func codebuffRunStateUsage(metadata map[string]any) *codebuffUsage {
	history := arrayAt(objectAt(objectAt(objectAt(metadata["runState"])["sessionState"])["mainAgentState"])["messageHistory"])
	if len(history) == 0 {
		return nil
	}
	var usage codebuffUsage
	found := false
	for i := len(history) - 1; i >= 0; i-- {
		entry := objectAt(history[i])
		if stringField(entry, "role") != "assistant" {
			continue
		}
		providerOptions := objectAt(entry["providerOptions"])
		if providerOptions == nil {
			continue
		}
		entryUsage := parseCodebuffUsageObject(providerOptions["usage"])
		codebuff := objectAt(providerOptions["codebuff"])
		if codebuff != nil {
			mergeCodebuffUsage(&entryUsage, parseCodebuffUsageObject(codebuff["usage"]))
			if entryUsage.model == "" {
				entryUsage.model = stringField(codebuff, "model")
			}
		}
		if codebuffUsageHasTokens(entryUsage) || entryUsage.model != "" {
			found = true
		}
		mergeCodebuffUsage(&usage, entryUsage)
	}
	if !found {
		return nil
	}
	return &usage
}

func parseCodebuffUsageObject(value any) codebuffUsage {
	record := objectAt(value)
	if record == nil {
		return codebuffUsage{}
	}
	parsed := codebuffUsage{
		model:                    stringField(record, "model"),
		inputTokens:              firstUint(record, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens"),
		outputTokens:             firstUint(record, "outputTokens", "output_tokens", "completionTokens", "completion_tokens"),
		cacheReadInputTokens:     firstUint(record, "cacheReadInputTokens", "cache_read_input_tokens"),
		cacheCreationInputTokens: firstUint(record, "cacheCreationInputTokens", "cache_creation_input_tokens", "cacheCreationTokens", "cache_creation_tokens", "cachedTokensCreated", "cached_tokens_created"),
	}
	parsed.cacheReadInputTokens = maxUint64(parsed.cacheReadInputTokens, firstUint(objectAt(record["promptTokensDetails"]), "cachedTokens"))
	parsed.cacheReadInputTokens = maxUint64(parsed.cacheReadInputTokens, firstUint(objectAt(record["prompt_tokens_details"]), "cached_tokens"))
	tokens := usage.TokenUsage{
		InputTokens:              parsed.inputTokens,
		OutputTokens:             parsed.outputTokens,
		CacheCreationInputTokens: parsed.cacheCreationInputTokens,
		CacheReadInputTokens:     parsed.cacheReadInputTokens,
	}
	tokens = applyTotalFallback(tokens, firstUint(record, "totalTokens", "total_tokens", "total"))
	parsed.inputTokens = tokens.InputTokens
	parsed.outputTokens = tokens.OutputTokens
	parsed.cacheCreationInputTokens = tokens.CacheCreationInputTokens
	parsed.cacheReadInputTokens = tokens.CacheReadInputTokens
	parsed.extraTotalTokens = tokens.ReasoningOutputTokens
	return parsed
}

func mergeCodebuffUsage(target *codebuffUsage, fallback codebuffUsage) {
	if target.inputTokens == 0 {
		target.inputTokens = fallback.inputTokens
	}
	if target.outputTokens == 0 {
		target.outputTokens = fallback.outputTokens
	}
	if target.cacheCreationInputTokens == 0 {
		target.cacheCreationInputTokens = fallback.cacheCreationInputTokens
	}
	if target.cacheReadInputTokens == 0 {
		target.cacheReadInputTokens = fallback.cacheReadInputTokens
	}
	if target.extraTotalTokens == 0 {
		target.extraTotalTokens = fallback.extraTotalTokens
	}
	if target.model == "" {
		target.model = fallback.model
	}
}

func codebuffUsageHasTokens(value codebuffUsage) bool {
	return value.inputTokens > 0 || value.outputTokens > 0 || value.cacheCreationInputTokens > 0 || value.cacheReadInputTokens > 0 || value.extraTotalTokens > 0
}

func codebuffMessageTimestamp(message map[string]any) (time.Time, bool) {
	if timestamp, ok := parseTimestamp(message["timestamp"]); ok {
		return timestamp, true
	}
	if timestamp, ok := parseTimestamp(message["createdAt"]); ok {
		return timestamp, true
	}
	return parseTimestamp(objectAt(message["metadata"])["timestamp"])
}

func parseCodebuffChatTimestamp(chatID string) (time.Time, bool) {
	date, clock, ok := strings.Cut(chatID, "T")
	if !ok {
		return time.Time{}, false
	}
	for i := 0; i < 2; i++ {
		if index := strings.Index(clock, "-"); index >= 0 {
			clock = clock[:index] + ":" + clock[index+1:]
		}
	}
	return parseTimestampString(date + "T" + clock)
}

func codebuffDedupKey(message map[string]any, sessionID string, timestamp time.Time, model string, tokens usage.TokenUsage, index int) string {
	if id := stringField(message, "id"); id != "" {
		return "codebuff:" + sessionID + ":" + id
	}
	return stableEntryID(baseEntry(usage.ProviderCodebuff, timestamp, "codebuff", "Codebuff", sessionID, model, "Codebuff", tokens), strconv.Itoa(index))
}

func firstUint(record map[string]any, keys ...string) uint64 {
	if record == nil {
		return 0
	}
	return uintField(record, keys...)
}

func maxUint64(a, b uint64) uint64 {
	if b > a {
		return b
	}
	return a
}
