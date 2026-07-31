package openclaw

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
		files = append(files, shared.CollectFiles(path, isSessionFile)...)
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

func isSessionFile(path string) bool {
	name := filepath.Base(path)
	index := strings.Index(name, ".jsonl")
	if index < 0 {
		return false
	}
	suffix := name[index:]
	return suffix == ".jsonl" || strings.HasPrefix(suffix, ".jsonl.deleted.") || strings.HasPrefix(suffix, ".jsonl.reset.")
}

func parseSessionFile(path string) ([]usage.Entry, error) {
	lines, err := shared.ReadJSONLines(path)
	if err != nil {
		return nil, err
	}
	sessionID := openClawSessionID(path)
	currentModel := ""
	currentProvider := ""
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.Value
		if isModelChange(record) {
			source := shared.ObjectAt(record["data"])
			if source == nil {
				source = record
			}
			if model := shared.FirstStringField(source, "modelId", "model"); model != "" {
				currentModel = model
			}
			if provider := shared.StringField(source, "provider"); provider != "" {
				currentProvider = provider
			}
			continue
		}
		if shared.StringField(record, "type") != "message" {
			continue
		}
		message := shared.ObjectAt(record["message"])
		if shared.StringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := shared.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := shared.ParseTimestamp(message["timestamp"])
		if !ok {
			timestamp, ok = shared.ParseTimestamp(record["timestamp"])
		}
		if !ok {
			timestamp = shared.FileModifiedTime(path)
		}
		model := shared.FirstStringField(message, "modelId", "model")
		if model == "" {
			model = currentModel
		}
		if model == "" {
			model = "unknown"
		}
		provider := shared.StringField(message, "provider")
		if provider == "" {
			provider = currentProvider
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
		entry := shared.BaseEntry(usage.ProviderOpenClaw, timestamp, "openclaw", "OpenClaw", sessionID, "[openclaw] "+model, "OpenClaw", tokens)
		shared.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = shared.StableEntryID(entry, provider)
		entries = append(entries, entry)
	}
	return entries, nil
}

func isModelChange(record map[string]any) bool {
	if shared.StringField(record, "type") == "model_change" {
		return true
	}
	return shared.StringField(record, "type") == "custom" && shared.StringField(record, "customType") == "model-snapshot"
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
