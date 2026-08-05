package openclaw

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// openclaw is deliberately not resumable: a model-change line sets the model
// for every message after it, so parsing from a mid-file offset would attribute
// later entries to "unknown" instead. The state would have to be carried across
// scans to resume safely, and it is not worth that for this provider's volume.
func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, path := range paths {
		files = append(files, agentdata.CollectFiles(path, isSessionFile)...)
	}
	sort.Strings(files)
	files = agentdata.FilterFiles(files, filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseSessionFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
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
	lines, err := agentdata.ReadJSONLines(path)
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
			source := agentdata.ObjectAt(record["data"])
			if source == nil {
				source = record
			}
			if model := agentdata.FirstStringField(source, "modelId", "model"); model != "" {
				currentModel = model
			}
			if provider := agentdata.StringField(source, "provider"); provider != "" {
				currentProvider = provider
			}
			continue
		}
		if agentdata.StringField(record, "type") != "message" {
			continue
		}
		message := agentdata.ObjectAt(record["message"])
		if agentdata.StringField(message, "role") != "assistant" {
			continue
		}
		usageBlock := agentdata.ObjectAt(message["usage"])
		if usageBlock == nil {
			continue
		}
		timestamp, ok := agentdata.ParseTimestamp(message["timestamp"])
		if !ok {
			timestamp, ok = agentdata.ParseTimestamp(record["timestamp"])
		}
		if !ok {
			timestamp = agentdata.FileModifiedTime(path)
		}
		model := agentdata.FirstStringField(message, "modelId", "model")
		if model == "" {
			model = currentModel
		}
		if model == "" {
			model = "unknown"
		}
		provider := agentdata.StringField(message, "provider")
		if provider == "" {
			provider = currentProvider
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
		entry := usageprovider.BaseEntry(usage.ProviderOpenClaw, timestamp, "openclaw", "OpenClaw", sessionID, "[openclaw] "+model, "OpenClaw", tokens)
		usageprovider.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = usageprovider.StableEntryID(entry, provider)
		entries = append(entries, entry)
	}
	return entries, nil
}

func isModelChange(record map[string]any) bool {
	if agentdata.StringField(record, "type") == "model_change" {
		return true
	}
	return agentdata.StringField(record, "type") == "custom" && agentdata.StringField(record, "customType") == "model-snapshot"
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
