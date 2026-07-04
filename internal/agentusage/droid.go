package agentusage

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func loadDroidEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectFiles(root, isDroidSettingsFile)...)
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		entry, ok, err := parseDroidSettingsFile(file)
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, entry)
		}
	}
	sortEntries(entries)
	return entries, nil
}

func isDroidSettingsFile(path string) bool {
	return strings.HasSuffix(filepath.Base(path), ".settings.json")
}

func parseDroidSettingsFile(path string) (usage.Entry, bool, error) {
	settings, err := readJSONObject(path)
	if err != nil || settings == nil {
		return usage.Entry{}, false, err
	}
	usageBlock := objectAt(settings["tokenUsage"])
	if usageBlock == nil {
		return usage.Entry{}, false, nil
	}
	tokens := usage.TokenUsage{
		InputTokens:              uintField(usageBlock, "inputTokens"),
		OutputTokens:             uintField(usageBlock, "outputTokens"),
		CacheCreationInputTokens: uintField(usageBlock, "cacheCreationTokens"),
		CacheReadInputTokens:     uintField(usageBlock, "cacheReadTokens"),
		ReasoningOutputTokens:    uintField(usageBlock, "thinkingTokens"),
	}
	tokens = applyTotalFallback(tokens, uintField(usageBlock, "totalTokens"))
	if !nonZero(tokens) {
		return usage.Entry{}, false, nil
	}

	provider := normalizeDroidProvider(stringField(settings, "providerLock"))
	model := normalizeDroidModel(stringField(settings, "model"))
	if model == "" {
		model, _ = droidSidecarModel(path)
	}
	if model == "" {
		model = droidDefaultModel(provider)
	}
	if model == "" {
		model = "unknown"
	}

	timestamp, ok := parseTimestamp(settings["providerLockTimestamp"])
	if !ok {
		timestamp = fileModifiedTime(path)
	}
	sessionID := strings.TrimSuffix(filepath.Base(path), ".settings.json")
	if sessionID == "" {
		sessionID = "unknown"
	}
	entry := baseEntry(usage.ProviderDroid, timestamp, "droid", "Droid", sessionID, model, "Droid", tokens)
	setSource(&entry, path, 1, 0, 0)
	entry.ID = stableEntryID(entry, sessionID)
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

func droidDefaultModel(provider string) string {
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

func droidSidecarModel(settingsPath string) (string, error) {
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
		if model := droidModelFromLine(scanner.Text()); model != "" {
			return model, nil
		}
	}
	return "", scanner.Err()
}

func droidModelFromLine(line string) string {
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
