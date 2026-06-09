package codexusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labx/tracklm-goagent/internal/usage"
)

var ErrNoDataDirs = errors.New("no valid Codex data directories found")

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID         string `json:"id"`
	CWD        string `json:"cwd"`
	Originator string `json:"originator"`
}

type turnContextPayload struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

type eventPayload struct {
	Type string `json:"type"`
	Info struct {
		LastTokenUsage *tokenUsagePayload `json:"last_token_usage"`
	} `json:"info"`
}

type tokenUsagePayload struct {
	InputTokens           uint64 `json:"input_tokens"`
	CachedInputTokens     uint64 `json:"cached_input_tokens"`
	OutputTokens          uint64 `json:"output_tokens"`
	ReasoningOutputTokens uint64 `json:"reasoning_output_tokens"`
	TotalTokens           uint64 `json:"total_tokens"`
}

func LoadEntries(projectFilter string) ([]usage.Entry, error) {
	paths, err := CodexPaths()
	if err != nil {
		return nil, err
	}
	return LoadEntriesFromPaths(paths, projectFilter)
}

func DailyProjectSummaries(projectFilter string) ([]usage.DailyProjectSummary, error) {
	entries, err := LoadEntries(projectFilter)
	if err != nil {
		return nil, err
	}
	return usage.SummarizeDailyProjects(entries), nil
}

func LoadEntriesFromPaths(paths []string, projectFilter string) ([]usage.Entry, error) {
	files := UsageFiles(paths)
	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := ReadUsageFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if projectFilter != "" && entry.Project != projectFilter && entry.ProjectPath != projectFilter {
				continue
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func CodexPaths() ([]string, error) {
	if envPaths, ok := os.LookupEnv("CODEX_CONFIG_DIR"); ok {
		return codexPathsFromCSV(envPaths)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("home directory is not set")
	}
	return codexPathsFromCSV(filepath.Join(home, ".codex"))
}

func codexPathsFromCSV(value string) ([]string, error) {
	paths := make([]string, 0)
	seen := map[string]struct{}{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		path := filepath.Clean(expandHomePath(raw))
		if !dirExists(filepath.Join(path, "sessions")) && !dirExists(filepath.Join(path, "archived_sessions")) {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, ErrNoDataDirs
	}
	return paths, nil
}

func UsageFiles(paths []string) []string {
	files := make([]string, 0)
	for _, path := range paths {
		collectJSONLFiles(filepath.Join(path, "sessions"), &files)
		collectJSONLFiles(filepath.Join(path, "archived_sessions"), &files)
	}
	sort.Strings(files)
	return files
}

func ReadUsageFile(path string) ([]usage.Entry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	state := fileState{
		sessionID: sessionIDFromFilename(path),
	}
	entries := make([]usage.Entry, 0)
	reader := bufio.NewReader(file)
	lineNumber := 0
	offset := int64(0)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			start := offset
			offset += int64(len(line))
			line = bytes.TrimRight(line, "\r\n")
			if entry, ok := parseLine(line, &state); ok {
				entry.SourceFile = path
				entry.SourceLine = lineNumber
				entry.SourceStart = start
				entry.SourceEnd = offset
				entry.ID = stableEntryID(entry)
				entries = append(entries, entry)
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return nil, readErr
	}
	return entries, nil
}

type fileState struct {
	sessionID   string
	projectPath string
	model       string
	client      string
}

func parseLine(line []byte, state *fileState) (usage.Entry, bool) {
	if !bytes.Contains(line, []byte(`"type"`)) {
		return usage.Entry{}, false
	}

	var envelope codexLine
	if err := json.Unmarshal(line, &envelope); err != nil {
		return usage.Entry{}, false
	}

	switch envelope.Type {
	case "session_meta":
		var payload sessionMetaPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return usage.Entry{}, false
		}
		if strings.TrimSpace(payload.ID) != "" {
			state.sessionID = payload.ID
		}
		if strings.TrimSpace(payload.CWD) != "" {
			state.projectPath = payload.CWD
		}
		if client := usage.NormalizeClient(usage.ProviderCodex, payload.Originator); client != "" {
			state.client = client
		}
		return usage.Entry{}, false
	case "turn_context":
		var payload turnContextPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return usage.Entry{}, false
		}
		if strings.TrimSpace(payload.CWD) != "" {
			state.projectPath = payload.CWD
		}
		if strings.TrimSpace(payload.Model) != "" {
			state.model = payload.Model
		}
		return usage.Entry{}, false
	case "event_msg":
		var payload eventPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return usage.Entry{}, false
		}
		if payload.Type != "token_count" || payload.Info.LastTokenUsage == nil {
			return usage.Entry{}, false
		}
		timestamp, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err != nil {
			return usage.Entry{}, false
		}
		tokens := payload.Info.LastTokenUsage

		// Codex reports input_tokens as the FULL prompt (cached + non-cached),
		// with cached_input_tokens being the cached portion. Match ccusage:
		// real input = input_tokens - cached, and the cached part is cache read.
		// Otherwise the cached prompt gets double-counted into input.
		nonCachedInput := tokens.InputTokens
		if tokens.CachedInputTokens <= tokens.InputTokens {
			nonCachedInput = tokens.InputTokens - tokens.CachedInputTokens
		} else {
			nonCachedInput = 0
		}

		return usage.Entry{
			Provider:    usage.ProviderCodex,
			Timestamp:   timestamp,
			Date:        timestamp.In(time.Local).Format("2006-01-02"),
			Project:     projectName(state.projectPath),
			ProjectPath: state.projectPath,
			SessionID:   state.sessionID,
			Model:       state.model,
			OS:          usage.NormalizeOS(runtime.GOOS),
			Client:      state.client,
			Usage: usage.TokenUsage{
				InputTokens:           nonCachedInput,
				CacheReadInputTokens:  tokens.CachedInputTokens,
				OutputTokens:          tokens.OutputTokens,
				ReasoningOutputTokens: tokens.ReasoningOutputTokens,
				TotalTokens:           tokens.TotalTokens,
			},
		}, true
	default:
		return usage.Entry{}, false
	}
}

func stableEntryID(entry usage.Entry) string {
	return usage.StableID(
		string(usage.ProviderCodex),
		entry.SourceFile,
		strconv.Itoa(entry.SourceLine),
		entry.Timestamp.Format(time.RFC3339Nano),
		entry.Model,
		strconv.FormatUint(entry.Usage.InputTokens, 10),
		strconv.FormatUint(entry.Usage.CachedInputTokens, 10),
		strconv.FormatUint(entry.Usage.OutputTokens, 10),
		strconv.FormatUint(entry.Usage.ReasoningOutputTokens, 10),
		strconv.FormatUint(entry.Usage.TotalTokens, 10),
	)
}

func projectName(path string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "unknown"
	}
	base := filepath.Base(filepath.Clean(clean))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "unknown"
	}
	return base
}

func sessionIDFromFilename(path string) string {
	base := filepath.Base(path)
	sessionID := strings.TrimSuffix(base, filepath.Ext(base))
	sessionID = strings.TrimPrefix(sessionID, "rollout-")
	if strings.TrimSpace(sessionID) == "" {
		return "unknown"
	}
	return sessionID
}

func collectJSONLFiles(dir string, files *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			collectJSONLFiles(path, files)
			continue
		}
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			*files = append(*files, path)
		}
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func expandHomePath(raw string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return raw
	}
	if raw == "~" {
		return home
	}
	if strings.HasPrefix(raw, "~/") {
		return filepath.Join(home, strings.TrimPrefix(raw, "~/"))
	}
	return raw
}
