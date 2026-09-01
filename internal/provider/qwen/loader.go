package qwen

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func chatFiles(paths []string) []string {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, agentdata.CollectFiles(filepath.Join(root, "projects"), isChatFile)...)
		if strings.Contains(filepath.ToSlash(root), "/projects/") {
			files = append(files, agentdata.CollectFiles(root, isChatFile)...)
		}
	}
	sort.Strings(files)
	return agentdata.UniqueStrings(files)
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := agentdata.FilterFiles(chatFiles(paths), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, _, err := parseChatFileFrom(file, 0)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
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

func parseChatFileFrom(path string, start int64) ([]usage.Entry, int64, error) {
	lines, consumed, err := agentdata.ReadJSONLinesFrom(path, start, `"usageMetadata"`)
	if err != nil {
		return nil, 0, err
	}
	project := project(path)
	fallback := agentdata.FileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.Value
		if agentdata.StringField(record, "type") != "assistant" {
			continue
		}
		meta := agentdata.ObjectAt(record["usageMetadata"])
		if meta == nil {
			continue
		}
		timestamp, ok := agentdata.ParseTimestamp(record["timestamp"])
		if !ok {
			timestamp = fallback
		}
		sessionID := agentdata.StringField(record, "sessionId")
		if sessionID == "" {
			sessionID = project + "-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		model := agentdata.StringField(record, "model")
		if model == "" {
			model = "unknown"
		}
		prompt := agentdata.UintField(meta, "promptTokenCount")
		output := agentdata.UintField(meta, "candidatesTokenCount")
		thoughts := agentdata.UintField(meta, "thoughtsTokenCount")
		cached := agentdata.UintField(meta, "cachedContentTokenCount")
		total := agentdata.UintField(meta, "totalTokenCount")
		// Google-shaped usage: promptTokenCount may already include the
		// cached tokens. Same disambiguation as the Gemini provider — the
		// overlap must not be billed at both the input and cache-read rate.
		input, cacheRead := usageprovider.SubtractCachedOverlap(
			prompt, output, thoughts, 0, cached, total, total > 0)
		tokens := usage.TokenUsage{
			InputTokens:           input,
			OutputTokens:          output,
			CacheReadInputTokens:  cacheRead,
			ReasoningOutputTokens: thoughts,
		}
		tokens = usageprovider.ApplyTotalFallback(tokens, total)
		if !usageprovider.NonZero(tokens) {
			continue
		}
		entry := usageprovider.BaseEntry(usage.ProviderQwen, timestamp, "qwen", project, sessionID, model, "Qwen", tokens)
		usageprovider.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = usageprovider.StableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, consumed, nil
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
