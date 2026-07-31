package droid

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, shared.CollectFiles(root, isSettingsFile)...)
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		entry, ok, err := parseSettingsFile(file)
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, entry)
		}
	}
	shared.SortEntries(entries)
	return entries, nil
}

func isSettingsFile(path string) bool {
	return strings.HasSuffix(filepath.Base(path), ".settings.json")
}

func parseSettingsFile(path string) (usage.Entry, bool, error) {
	settings, err := shared.ReadJSONObject(path)
	if err != nil || settings == nil {
		return usage.Entry{}, false, err
	}
	usageBlock := shared.ObjectAt(settings["tokenUsage"])
	if usageBlock == nil {
		return usage.Entry{}, false, nil
	}
	tokens := usage.TokenUsage{
		InputTokens:              shared.UintField(usageBlock, "inputTokens"),
		OutputTokens:             shared.UintField(usageBlock, "outputTokens"),
		CacheCreationInputTokens: shared.UintField(usageBlock, "cacheCreationTokens"),
		CacheReadInputTokens:     shared.UintField(usageBlock, "cacheReadTokens"),
		ReasoningOutputTokens:    shared.UintField(usageBlock, "thinkingTokens"),
	}
	tokens = shared.ApplyTotalFallback(tokens, shared.UintField(usageBlock, "totalTokens"))
	if !shared.NonZero(tokens) {
		return usage.Entry{}, false, nil
	}

	provider := normalizeDroidProvider(shared.StringField(settings, "providerLock"))
	model := normalizeDroidModel(shared.StringField(settings, "model"))
	if model == "" {
		model, _ = sidecarModel(path)
	}
	if model == "" {
		model = defaultModel(provider)
	}
	if model == "" {
		model = "unknown"
	}

	timestamp, ok := shared.ParseTimestamp(settings["providerLockTimestamp"])
	if !ok {
		timestamp = shared.FileModifiedTime(path)
	}
	sessionID := strings.TrimSuffix(filepath.Base(path), ".settings.json")
	if sessionID == "" {
		sessionID = "unknown"
	}
	entry := shared.BaseEntry(usage.ProviderDroid, timestamp, "droid", "Droid", sessionID, model, "Droid", tokens)
	shared.SetSource(&entry, path, 1, 0, 0)
	entry.ID = shared.StableEntryID(entry, sessionID)
	return entry, true, nil
}

func normalizeDroidModel(model string) string {
	raw := strings.TrimSpace(strings.TrimPrefix(model, "custom:"))
	if raw == "" {
		return ""
	}
	var withoutBrackets strings.Builder
	depth := 0
	for _, ch := range raw {
		switch ch {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				withoutBrackets.WriteRune(ch)
			}
		}
	}
	lower := strings.ToLower(strings.TrimRight(strings.TrimSpace(withoutBrackets.String()), "-"))
	var normalized strings.Builder
	previousDash := false
	for _, ch := range lower {
		next := ch
		if ch == '.' || ch == '-' || ch == '_' || ch == ' ' || ch == '\t' || ch == '\n' {
			next = '-'
		}
		if next == '-' {
			if !previousDash {
				normalized.WriteRune('-')
				previousDash = true
			}
			continue
		}
		normalized.WriteRune(next)
		previousDash = false
	}
	return strings.Trim(normalized.String(), "-")
}

func normalizeDroidProvider(provider string) string {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(provider)), "-", "_")
	switch normalized {
	case "", "unknown":
		return "unknown"
	case "claude", "anthropic":
		return "anthropic"
	case "google", "google_ai", "gemini", "vertex", "vertex_ai":
		return "google"
	case "xai", "x_ai", "grok":
		return "xai"
	default:
		return normalized
	}
}

func defaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-unknown"
	case "openai":
		return "gpt-unknown"
	case "google":
		return "gemini-unknown"
	case "xai":
		return "grok-unknown"
	default:
		return "unknown"
	}
}

func sidecarModel(settingsPath string) (string, error) {
	name := filepath.Base(settingsPath)
	prefix := strings.TrimSuffix(name, ".settings.json")
	if prefix == name || prefix == "" {
		return "", nil
	}
	file, err := os.Open(filepath.Join(filepath.Dir(settingsPath), prefix+".jsonl"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for i := 0; i < 500 && scanner.Scan(); i++ {
		if model := modelFromLine(scanner.Text()); model != "" {
			return model, nil
		}
	}
	return "", scanner.Err()
}

func modelFromLine(line string) string {
	_, tail, ok := strings.Cut(line, "Model:")
	if !ok {
		return ""
	}
	parts := strings.FieldsFunc(tail, func(r rune) bool {
		return r == '"' || r == '\\' || r == '['
	})
	if len(parts) == 0 {
		return ""
	}
	raw := strings.TrimSpace(parts[0])
	if raw == "" {
		return ""
	}
	return normalizeDroidModel(raw)
}
