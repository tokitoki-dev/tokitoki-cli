package qwen

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, shared.CollectFiles(filepath.Join(root, "projects"), isChatFile)...)
		if strings.Contains(filepath.ToSlash(root), "/projects/") {
			files = append(files, shared.CollectFiles(root, isChatFile)...)
		}
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseChatFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	shared.SortEntries(entries)
	return entries, nil
}

func isChatFile(path string) bool {
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

func parseChatFile(path string) ([]usage.Entry, error) {
	lines, err := shared.ReadJSONLines(path, `"usageMetadata"`)
	if err != nil {
		return nil, err
	}
	project := project(path)
	fallback := shared.FileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.Value
		if shared.StringField(record, "type") != "assistant" {
			continue
		}
		meta := shared.ObjectAt(record["usageMetadata"])
		if meta == nil {
			continue
		}
		timestamp, ok := shared.ParseTimestamp(record["timestamp"])
		if !ok {
			timestamp = fallback
		}
		sessionID := shared.StringField(record, "sessionId")
		if sessionID == "" {
			sessionID = project + "-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		model := shared.StringField(record, "model")
		if model == "" {
			model = "unknown"
		}
		tokens := usage.TokenUsage{
			InputTokens:          shared.UintField(meta, "promptTokenCount"),
			OutputTokens:         shared.UintField(meta, "candidatesTokenCount"),
			CacheReadInputTokens: shared.UintField(meta, "cachedContentTokenCount"),
			ReasoningOutputTokens: shared.UintField(meta,
				"thoughtsTokenCount",
			),
		}
		tokens = shared.ApplyTotalFallback(tokens, shared.UintField(meta, "totalTokenCount"))
		if !shared.NonZero(tokens) {
			continue
		}
		entry := shared.BaseEntry(usage.ProviderQwen, timestamp, "qwen", project, sessionID, model, "Qwen", tokens)
		shared.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = shared.StableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func project(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "projects" && parts[i+2] == "chats" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return usage.UnknownProject
}
