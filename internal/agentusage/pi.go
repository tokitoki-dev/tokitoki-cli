package agentusage

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadPiEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		files = append(files, collectExt(path, ".jsonl")...)
	}
	sort.Strings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parsePiSessionFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func parsePiSessionFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path, `"usage"`, `"message"`)
	if err != nil {
		return nil, err
	}
	project := piProject(path)
	sessionID := piSessionID(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		if typ := stringField(line.value, "type"); typ != "" && typ != "message" {
			continue
		}
		message := objectAt(line.value["message"])
		if stringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := objectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := parseTimestamp(line.value["timestamp"])
		if !ok {
			continue
		}
		tokens := usage.TokenUsage{
			InputTokens:              uintField(usageBlock, "input"),
			OutputTokens:             uintField(usageBlock, "output"),
			CacheCreationInputTokens: uintField(usageBlock, "cacheWrite"),
			CacheReadInputTokens:     uintField(usageBlock, "cacheRead"),
		}
		tokens = applyTotalFallback(tokens, uintField(usageBlock, "totalTokens"))
		if !nonZero(tokens) {
			continue
		}
		model := stringField(message, "model")
		if model != "" {
			model = "[pi] " + model
		}
		entry := baseEntry(usage.ProviderPi, timestamp, project, project, sessionID, model, "pi-agent", tokens)
		setSource(&entry, path, line.line, line.start, line.end)
		entry.ID = stableEntryID(entry)
		entries = append(entries, entry)
	}
	return entries, nil
}

func piSessionID(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if before, after, ok := strings.Cut(stem, "_"); ok && before != "" && after != "" {
		return after
	}
	if stem == "" {
		return "unknown"
	}
	return stem
}

func piProject(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, part := range parts {
		if part == "sessions" && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return "unknown"
}
