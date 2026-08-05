package pi

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func sessionFiles(paths []string) []string {
	files := make([]string, 0)
	for _, path := range paths {
		files = append(files, agentdata.CollectExt(path, ".jsonl")...)
	}
	sort.Strings(files)
	return files
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := agentdata.FilterFiles(sessionFiles(paths), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, _, err := parseSessionFileFrom(file, 0)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

func parseSessionFileFrom(path string, start int64) ([]usage.Entry, int64, error) {
	lines, consumed, err := agentdata.ReadJSONLinesFrom(path, start, `"usage"`, `"message"`)
	if err != nil {
		return nil, 0, err
	}
	project := project(path)
	sessionID := sessionID(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		if typ := agentdata.StringField(line.Value, "type"); typ != "" && typ != "message" {
			continue
		}
		message := agentdata.ObjectAt(line.Value["message"])
		if agentdata.StringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := agentdata.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := agentdata.ParseTimestamp(line.Value["timestamp"])
		if !ok {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              agentdata.UintField(usageBlock, "input"),
			OutputTokens:             agentdata.UintField(usageBlock, "output"),
			CacheCreationInputTokens: agentdata.UintField(usageBlock, "cacheWrite"),
			CacheReadInputTokens:     agentdata.UintField(usageBlock, "cacheRead"),
		}
		tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(usageBlock, "totalTokens"))
		if !usageprovider.NonZero(tokens) {
			continue
		}
		model := agentdata.StringField(message, "model")
		if model != "" {
			model = "[pi] " + model
		}
		entry := usageprovider.BaseEntry(usage.ProviderPi, timestamp, project, project, sessionID, model, "pi-agent", tokens)
		usageprovider.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = usageprovider.StableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, consumed, nil
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
