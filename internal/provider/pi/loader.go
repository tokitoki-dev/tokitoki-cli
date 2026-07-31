package pi

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		files = append(files, shared.CollectExt(path, ".jsonl")...)
	}
	sort.Strings(files)
	files = shared.FilterFiles(files, filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseSessionFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	shared.SortEntries(entries)
	return entries, nil
}

func parseSessionFile(path string) ([]usage.Entry, error) {
	lines, err := shared.ReadJSONLines(path, `"usage"`, `"message"`)
	if err != nil {
		return nil, err
	}
	project := project(path)
	sessionID := sessionID(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		if typ := shared.StringField(line.Value, "type"); typ != "" && typ != "message" {
			continue
		}
		message := shared.ObjectAt(line.Value["message"])
		if shared.StringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := shared.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := shared.ParseTimestamp(line.Value["timestamp"])
		if !ok {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              shared.UintField(usageBlock, "input"),
			OutputTokens:             shared.UintField(usageBlock, "output"),
			CacheCreationInputTokens: shared.UintField(usageBlock, "cacheWrite"),
			CacheReadInputTokens:     shared.UintField(usageBlock, "cacheRead"),
		}
		tokens = shared.ApplyTotalFallback(tokens, shared.UintField(usageBlock, "totalTokens"))
		if !shared.NonZero(tokens) {
			continue
		}
		model := shared.StringField(message, "model")
		if model != "" {
			model = "[pi] " + model
		}
		entry := shared.BaseEntry(usage.ProviderPi, timestamp, project, project, sessionID, model, "pi-agent", tokens)
		shared.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = shared.StableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func sessionID(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if before, after, ok := strings.Cut(stem, "_"); ok && before != "" && after != "" {
		return after
	}
	if stem == "" {
		return "unknown"
	}
	return stem
}

func project(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, part := range parts {
		if part == "sessions" && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return usage.UnknownProject
}
