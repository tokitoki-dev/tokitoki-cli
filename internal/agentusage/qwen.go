package agentusage

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadQwenEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectFiles(filepath.Join(root, "projects"), isQwenChatFile)...)
		if strings.Contains(filepath.ToSlash(root), "/projects/") {
			files = append(files, collectFiles(root, isQwenChatFile)...)
		}
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseQwenChatFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func isQwenChatFile(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".jsonl") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "projects" && parts[i+2] == "chats" {
			return true
		}
	}
	return false
}

func parseQwenChatFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path, `"usageMetadata"`)
	if err != nil {
		return nil, err
	}
	project := qwenProject(path)
	fallback := fileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.value
		if stringField(record, "type") != "assistant" {
			continue
		}
		meta := objectAt(record["usageMetadata"])
		if meta == nil {
			continue
		}
		timestamp, ok := parseTimestamp(record["timestamp"])
		if !ok {
			timestamp = fallback
		}
		sessionID := stringField(record, "sessionId")
		if sessionID == "" {
			sessionID = project + "-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		model := stringField(record, "model")
		if model == "" {
			model = "unknown"
		}
		tokens := usage.TokenUsage{
			InputTokens:          uintField(meta, "promptTokenCount"),
			OutputTokens:         uintField(meta, "candidatesTokenCount"),
			CacheReadInputTokens: uintField(meta, "cachedContentTokenCount"),
			ReasoningOutputTokens: uintField(meta,
				"thoughtsTokenCount",
			),
		}
		tokens = applyTotalFallback(tokens, uintField(meta, "totalTokenCount"))
		if !nonZero(tokens) {
			continue
		}
		entry := baseEntry(usage.ProviderQwen, timestamp, "qwen", project, sessionID, model, "Qwen", tokens)
		setSource(&entry, path, line.line, line.start, line.end)
		entry.ID = stableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func qwenProject(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "projects" && parts[i+2] == "chats" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return "unknown"
}
