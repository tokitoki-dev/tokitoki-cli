package codebuff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

type tokenUsage struct {
	model                    string
	inputTokens              uint64
	outputTokens             uint64
	cacheCreationInputTokens uint64
	cacheReadInputTokens     uint64
	extraTotalTokens         uint64
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectChatFiles(root)...)
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entriesByID := make(map[string]usage.Entry)
	for _, file := range files {
		fileEntries, err := parseChatFile(file)
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
	shared.SortEntries(entries)
	return entries, nil
}

func collectChatFiles(root string) []string {
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
	return shared.CollectFiles(projectRoot, func(path string) bool {
		return filepath.Base(path) == "chat-messages.json"
	})
}

func parseChatFile(path string) ([]usage.Entry, error) {
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
	sessionID, chatID := sessionContext(path)
	chatTimestamp, hasChatTimestamp := parseChatTimestamp(chatID)
	fileTimestamp := shared.FileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for index, raw := range messages {
		message := shared.ObjectAt(raw)
		if !isAssistant(message) {
			continue
		}
		parsedUsage := extractUsage(message)
		tokens := usage.TokenUsage{
			InputTokens:              parsedUsage.inputTokens,
			OutputTokens:             parsedUsage.outputTokens,
			CacheCreationInputTokens: parsedUsage.cacheCreationInputTokens,
			CacheReadInputTokens:     parsedUsage.cacheReadInputTokens,
			ReasoningOutputTokens:    parsedUsage.extraTotalTokens,
		}
		if tokens.TotalTokens == 0 {
			tokens.TotalTokens = shared.TotalUsage(tokens)
		}
		if !shared.NonZero(tokens) {
			continue
		}
		model := parsedUsage.model
		if model == "" {
			model = "codebuff-unknown"
		}
		timestamp, ok := messageTimestamp(message)
		if !ok && hasChatTimestamp {
			timestamp = chatTimestamp
			ok = true
		}
		if !ok {
			timestamp = fileTimestamp
		}
		entry := shared.BaseEntry(usage.ProviderCodebuff, timestamp, "codebuff", "Codebuff", sessionID, model, "Codebuff", tokens)
		shared.SetSource(&entry, path, index+1, 0, 0)
		entry.ID = shared.StableEntryID(entry, dedupKey(message, sessionID, timestamp, model, tokens, index))
		entries = append(entries, entry)
	}
	return entries, nil
}

func sessionContext(path string) (string, string) {
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

func isAssistant(message map[string]any) bool {
	role := shared.FirstStringField(message, "variant", "role")
	return role == "ai" || role == "agent" || role == "assistant"
}

func extractUsage(message map[string]any) tokenUsage {
	var usage tokenUsage
	metadata := shared.ObjectAt(message["metadata"])
	if metadata != nil {
		usage.model = shared.StringField(metadata, "model")
		mergeCodebuffUsage(&usage, parseUsageObject(metadata["usage"]))
		mergeCodebuffUsage(&usage, parseUsageObject(shared.ObjectAt(metadata["codebuff"])["usage"]))
		if runState := runStateUsage(metadata); runState != nil {
			mergeCodebuffUsage(&usage, *runState)
		}
	}
	return usage
}

func runStateUsage(metadata map[string]any) *tokenUsage {
	history := shared.ArrayAt(shared.ObjectAt(shared.ObjectAt(shared.ObjectAt(metadata["runState"])["sessionState"])["mainAgentState"])["messageHistory"])
	if len(history) == 0 {
		return nil
	}
	var usage tokenUsage
	found := false
	for i := len(history) - 1; i >= 0; i-- {
		entry := shared.ObjectAt(history[i])
		if shared.StringField(entry, "role") != "assistant" {
			continue
		}
		providerOptions := shared.ObjectAt(entry["providerOptions"])
		if providerOptions == nil {
			continue
		}
		entryUsage := parseUsageObject(providerOptions["usage"])
		codebuff := shared.ObjectAt(providerOptions["codebuff"])
		if codebuff != nil {
			mergeCodebuffUsage(&entryUsage, parseUsageObject(codebuff["usage"]))
			if entryUsage.model == "" {
				entryUsage.model = shared.StringField(codebuff, "model")
			}
		}
		if usageHasTokens(entryUsage) || entryUsage.model != "" {
			found = true
		}
		mergeCodebuffUsage(&usage, entryUsage)
	}
	if !found {
		return nil
	}
	return &usage
}

func parseUsageObject(value any) tokenUsage {
	record := shared.ObjectAt(value)
	if record == nil {
		return tokenUsage{}
	}
	parsed := tokenUsage{
		model:                    shared.StringField(record, "model"),
		inputTokens:              firstUint(record, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens"),
		outputTokens:             firstUint(record, "outputTokens", "output_tokens", "completionTokens", "completion_tokens"),
		cacheReadInputTokens:     firstUint(record, "cacheReadInputTokens", "cache_read_input_tokens"),
		cacheCreationInputTokens: firstUint(record, "cacheCreationInputTokens", "cache_creation_input_tokens", "cacheCreationTokens", "cache_creation_tokens", "cachedTokensCreated", "cached_tokens_created"),
	}
	parsed.cacheReadInputTokens = maxUint64(parsed.cacheReadInputTokens, firstUint(shared.ObjectAt(record["promptTokensDetails"]), "cachedTokens"))
	parsed.cacheReadInputTokens = maxUint64(parsed.cacheReadInputTokens, firstUint(shared.ObjectAt(record["prompt_tokens_details"]), "cached_tokens"))
	tokens := usage.TokenUsage{
		InputTokens:              parsed.inputTokens,
		OutputTokens:             parsed.outputTokens,
		CacheCreationInputTokens: parsed.cacheCreationInputTokens,
		CacheReadInputTokens:     parsed.cacheReadInputTokens,
	}
	tokens = shared.ApplyTotalFallback(tokens, firstUint(record, "totalTokens", "total_tokens", "total"))
	parsed.inputTokens = tokens.InputTokens
	parsed.outputTokens = tokens.OutputTokens
	parsed.cacheCreationInputTokens = tokens.CacheCreationInputTokens
	parsed.cacheReadInputTokens = tokens.CacheReadInputTokens
	parsed.extraTotalTokens = tokens.ReasoningOutputTokens
	return parsed
}

func mergeCodebuffUsage(target *tokenUsage, fallback tokenUsage) {
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

func usageHasTokens(value tokenUsage) bool {
	return value.inputTokens > 0 || value.outputTokens > 0 || value.cacheCreationInputTokens > 0 || value.cacheReadInputTokens > 0 || value.extraTotalTokens > 0
}

func messageTimestamp(message map[string]any) (time.Time, bool) {
	if timestamp, ok := shared.ParseTimestamp(message["timestamp"]); ok {
		return timestamp, true
	}
	if timestamp, ok := shared.ParseTimestamp(message["createdAt"]); ok {
		return timestamp, true
	}
	return shared.ParseTimestamp(shared.ObjectAt(message["metadata"])["timestamp"])
}

func parseChatTimestamp(chatID string) (time.Time, bool) {
	date, clock, ok := strings.Cut(chatID, "T")
	if !ok {
		return time.Time{}, false
	}
	for i := 0; i < 2; i++ {
		if index := strings.Index(clock, "-"); index >= 0 {
			clock = clock[:index] + ":" + clock[index+1:]
		}
	}
	return shared.ParseTimestampString(date + "T" + clock)
}

func dedupKey(message map[string]any, sessionID string, timestamp time.Time, model string, tokens usage.TokenUsage, index int) string {
	if id := shared.StringField(message, "id"); id != "" {
		return "codebuff:" + sessionID + ":" + id
	}
	return shared.StableEntryID(shared.BaseEntry(usage.ProviderCodebuff, timestamp, "codebuff", "Codebuff", sessionID, model, "Codebuff", tokens), strconv.Itoa(index))
}

func firstUint(record map[string]any, keys ...string) uint64 {
	if record == nil {
		return 0
	}
	return shared.UintField(record, keys...)
}

func maxUint64(a, b uint64) uint64 {
	if b > a {
		return b
	}
	return a
}
