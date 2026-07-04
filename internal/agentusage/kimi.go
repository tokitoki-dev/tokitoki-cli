package agentusage

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadKimiEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectFiles(filepath.Join(root, "sessions"), isKimiWireFile)...)
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		fileEntries, err := parseKimiWireFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			entries = append(entries, entry)
		}
	}
	sortEntries(entries)
	return entries, nil
}

func isKimiWireFile(path string) bool {
	if filepath.Base(path) != "wire.jsonl" {
		return false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "sessions" && i+3 == len(parts)-1 {
			return true
		}
	}
	return false
}

func parseKimiWireFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path, `"StatusUpdate"`, `"token_usage"`)
	if err != nil {
		return nil, err
	}
	model := kimiModel(path)
	sessionID := filepath.Base(filepath.Dir(path))
	if sessionID == "" || sessionID == "." {
		sessionID = "unknown"
	}
	fallback := fileModifiedTime(path)
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		message := objectAt(line.value["message"])
		if stringField(message, "type") != "StatusUpdate" {
			continue
		}
		payload := objectAt(message["payload"])
		tokenUsage := objectAt(payload["token_usage"])
		if tokenUsage == nil {
			continue
		}
		timestamp, ok := parseTimestamp(line.value["timestamp"])
		if !ok {
			timestamp = fallback
		}
		tokens := usage.TokenUsage{
			InputTokens:              uintField(tokenUsage, "input_other"),
			OutputTokens:             uintField(tokenUsage, "output"),
			CacheCreationInputTokens: uintField(tokenUsage, "input_cache_creation"),
			CacheReadInputTokens:     uintField(tokenUsage, "input_cache_read"),
		}
		tokens = applyTotalFallback(tokens, uintField(tokenUsage, "total"))
		if !nonZero(tokens) {
			continue
		}
		messageID := stringField(payload, "message_id")
		entry := baseEntry(usage.ProviderKimi, timestamp, "kimi", "Kimi", sessionID, model, "Kimi", tokens)
		setSource(&entry, path, line.line, line.start, line.end)
		entry.ID = stableEntryID(entry, messageID)
		entries = append(entries, entry)
	}
	return entries, nil
}

func kimiModel(path string) string {
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path))))
	config, err := readJSONObject(filepath.Join(root, "config.json"))
	if err != nil || config == nil {
		return "kimi-for-coding"
	}
	if model := stringField(config, "model"); model != "" {
		return model
	}
	return "kimi-for-coding"
}
