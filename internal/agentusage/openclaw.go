package agentusage

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadOpenClawEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		files = append(files, collectFiles(path, isOpenClawSessionFile)...)
	}
	sort.Strings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseOpenClawSessionFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

func isOpenClawSessionFile(path string) bool {
	name := filepath.Base(path)
	index := strings.Index(name, ".jsonl")
	if index < 0 {
		return false
	}
	suffix := name[index:]
	return suffix == ".jsonl" || strings.HasPrefix(suffix, ".jsonl.deleted.") || strings.HasPrefix(suffix, ".jsonl.reset.")
}

func parseOpenClawSessionFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path)
	if err != nil {
		return nil, err
	}
	sessionID := openClawSessionID(path)
	currentModel := ""
	currentProvider := ""
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.value
		if isOpenClawModelChange(record) {
			source := objectAt(record["data"])
			if source == nil {
				source = record
			}
			if model := firstStringField(source, "modelId", "model"); model != "" {
				currentModel = model
			}
			if provider := stringField(source, "provider"); provider != "" {
				currentProvider = provider
			}
			continue
		}
		if stringField(record, "type") != "message" {
			continue
		}
		message := objectAt(record["message"])
		if stringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := objectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := parseTimestamp(message["timestamp"])
		if !ok {
			timestamp, ok = parseTimestamp(record["timestamp"])
		}
		if !ok {
			timestamp = fileModifiedTime(path)
		}
		model := firstStringField(message, "modelId", "model")
		if model == "" {
			model = currentModel
		}
		if model == "" {
			model = "unknown"
		}
		provider := stringField(message, "provider")
		if provider == "" {
			provider = currentProvider
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
		entry := baseEntry(usage.ProviderOpenClaw, timestamp, "openclaw", "OpenClaw", sessionID, "[openclaw] "+model, "OpenClaw", tokens)
		setSource(&entry, path, line.line, line.start, line.end)
		entry.ID = stableEntryID(entry, provider)
		entries = append(entries, entry)
	}
	return entries, nil
}

func isOpenClawModelChange(record map[string]any) bool {
	if stringField(record, "type") == "model_change" {
		return true
	}
	return stringField(record, "type") == "custom" && stringField(record, "customType") == "model-snapshot"
}

func openClawSessionID(path string) string {
	name := filepath.Base(path)
	index := strings.Index(name, ".jsonl")
	if index < 0 {
		if name == "" {
			return "unknown"
		}
		return name
	}
	if index == 0 {
		return name
	}
	return name[:index]
}
