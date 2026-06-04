package claudeusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

var ErrNoDataDirs = errors.New("no valid Claude data directories found")

type UsageEntry struct {
	SessionID         *string      `json:"sessionId"`
	Timestamp         string       `json:"timestamp"`
	Version           *string      `json:"version"`
	Entrypoint        *string      `json:"entrypoint"`
	Message           UsageMessage `json:"message"`
	CostUSD           *float64     `json:"costUSD"`
	RequestID         *string      `json:"requestId"`
	IsAPIErrorMessage *bool        `json:"isApiErrorMessage"`
}

type UsageMessage struct {
	Usage TokenUsage `json:"usage"`
	Model *string    `json:"model"`
	ID    *string    `json:"id"`
}

type TokenUsage struct {
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
	Speed                    *Speed `json:"speed"`
}

type Speed string

const (
	SpeedStandard Speed = "standard"
	SpeedFast     Speed = "fast"
)

func (s *Speed) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	speed := Speed(value)
	switch speed {
	case SpeedStandard, SpeedFast:
		*s = speed
		return nil
	default:
		return fmt.Errorf("unsupported Claude usage speed %q", value)
	}
}

type LoadedEntry struct {
	Data                UsageEntry `json:"data"`
	ID                  string     `json:"id,omitempty"`
	SourceFile          string     `json:"source_file,omitempty"`
	SourceLine          int        `json:"source_line,omitempty"`
	SourceStart         int64      `json:"source_start,omitempty"`
	SourceEnd           int64      `json:"source_end,omitempty"`
	Timestamp           time.Time  `json:"timestamp"`
	Date                string     `json:"date"`
	Project             string     `json:"project"`
	SessionID           string     `json:"session_id"`
	ProjectPath         string     `json:"project_path"`
	Model               string     `json:"model,omitempty"`
	Client              string     `json:"client,omitempty"`
	UsageLimitResetTime *time.Time `json:"usage_limit_reset_time,omitempty"`
}

type DailyProjectSummary struct {
	Date                     string `json:"date"`
	Project                  string `json:"project"`
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
	TotalTokens              uint64 `json:"total_tokens"`
}

func LoadEntries(projectFilter string) ([]LoadedEntry, error) {
	paths, err := ClaudePaths()
	if err != nil {
		return nil, err
	}
	return LoadEntriesFromPaths(paths, projectFilter)
}

func DailyProjectSummaries(projectFilter string) ([]DailyProjectSummary, error) {
	entries, err := LoadEntries(projectFilter)
	if err != nil {
		return nil, err
	}
	return SummarizeDailyProjects(entries), nil
}

func UsageEntries(projectFilter string) ([]usage.Entry, error) {
	entries, err := LoadEntries(projectFilter)
	if err != nil {
		return nil, err
	}
	return ConvertEntries(entries), nil
}

func UsageEntriesFromFile(path string) ([]usage.Entry, error) {
	entries, err := ReadUsageFile(path)
	if err != nil {
		return nil, err
	}
	return ConvertEntries(entries), nil
}

func ConvertEntries(entries []LoadedEntry) []usage.Entry {
	converted := make([]usage.Entry, 0, len(entries))
	for _, entry := range entries {
		tokens := entry.Data.Message.Usage
		converted = append(converted, usage.Entry{
			Provider:    usage.ProviderClaude,
			ID:          entry.ID,
			SourceFile:  entry.SourceFile,
			SourceLine:  entry.SourceLine,
			SourceStart: entry.SourceStart,
			SourceEnd:   entry.SourceEnd,
			Timestamp:   entry.Timestamp,
			Date:        entry.Date,
			Project:     entry.Project,
			ProjectPath: entry.ProjectPath,
			SessionID:   entry.SessionID,
			Model:       entry.Model,
			OS:          usage.NormalizeOS(runtime.GOOS),
			Client:      entry.Client,
			Usage: usage.TokenUsage{
				InputTokens:              tokens.InputTokens,
				OutputTokens:             tokens.OutputTokens,
				CacheCreationInputTokens: tokens.CacheCreationInputTokens,
				CacheReadInputTokens:     tokens.CacheReadInputTokens,
				TotalTokens:              tokenTotal(tokens),
			},
		})
	}
	return converted
}

func SummarizeDailyProjects(entries []LoadedEntry) []DailyProjectSummary {
	type key struct {
		date    string
		project string
	}
	indexes := map[key]int{}
	summaries := make([]DailyProjectSummary, 0)
	for _, entry := range entries {
		key := key{date: entry.Date, project: entry.Project}
		index, ok := indexes[key]
		if !ok {
			index = len(summaries)
			indexes[key] = index
			summaries = append(summaries, DailyProjectSummary{
				Date:    entry.Date,
				Project: entry.Project,
			})
		}
		usage := entry.Data.Message.Usage
		summary := &summaries[index]
		summary.InputTokens += usage.InputTokens
		summary.OutputTokens += usage.OutputTokens
		summary.CacheCreationInputTokens += usage.CacheCreationInputTokens
		summary.CacheReadInputTokens += usage.CacheReadInputTokens
		summary.TotalTokens += tokenTotal(usage)
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Project != summaries[j].Project {
			return summaries[i].Project < summaries[j].Project
		}
		return summaries[i].Date < summaries[j].Date
	})
	return summaries
}

func LoadEntriesFromPaths(paths []string, projectFilter string) ([]LoadedEntry, error) {
	files := UsageFiles(paths, projectFilter)
	entries := make([]LoadedEntry, 0)
	for _, file := range files {
		fileEntries, err := ReadUsageFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if projectFilter != "" && entry.Project != projectFilter {
				continue
			}
			entries = appendDeduped(entries, entry)
		}
	}
	return entries, nil
}

func ClaudePaths() ([]string, error) {
	if envPaths, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
		paths := make([]string, 0)
		seen := map[string]struct{}{}
		for _, raw := range strings.Split(envPaths, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			path := normalizeClaudeConfigPath(raw)
			if !dirExists(filepath.Join(path, "projects")) {
				continue
			}
			clean := filepath.Clean(path)
			if _, exists := seen[clean]; exists {
				continue
			}
			seen[clean] = struct{}{}
			paths = append(paths, clean)
		}
		if len(paths) > 0 {
			return paths, nil
		}
		return nil, fmt.Errorf("%w in CLAUDE_CONFIG_DIR: expected Claude config directories containing projects/, or projects/ directories: %s", ErrNoDataDirs, envPaths)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("home directory is not set")
	}

	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}

	candidates := []string{
		filepath.Join(xdg, "claude"),
		filepath.Join(home, ".claude"),
	}
	paths := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, path := range candidates {
		clean := filepath.Clean(path)
		if !dirExists(filepath.Join(clean, "projects")) {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	if len(paths) == 0 {
		return nil, ErrNoDataDirs
	}
	return paths, nil
}

func UsageFiles(paths []string, projectFilter string) []string {
	files := make([]string, 0)
	for _, path := range paths {
		projectsDir := filepath.Join(path, "projects")
		if isProjectPathSegment(projectFilter) {
			collectUsageFiles(filepath.Join(projectsDir, projectFilter), &files)
			continue
		}
		collectUsageFiles(projectsDir, &files)
	}
	sort.Strings(files)
	return files
}

func ReadUsageFile(path string) ([]LoadedEntry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	project := ExtractProject(path)
	sessionID, projectPath := ExtractSessionParts(path)
	entries := make([]LoadedEntry, 0)
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
			entry, ok := parseUsageLine(line, project, sessionID, projectPath)
			if ok {
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

func ExtractProject(path string) string {
	parts := pathParts(path)
	for i, part := range parts {
		if part != "projects" {
			continue
		}
		if i+1 >= len(parts) || strings.TrimSpace(parts[i+1]) == "" {
			return "unknown"
		}
		return parts[i+1]
	}
	return "unknown"
}

func ExtractSessionParts(path string) (string, string) {
	parts := pathParts(path)
	relative := parts
	for i, part := range parts {
		if part == "projects" {
			relative = parts[i+1:]
			break
		}
	}

	fileSessionID := ""
	if len(relative) > 0 {
		fileSessionID = strings.TrimSuffix(relative[len(relative)-1], ".jsonl")
		if fileSessionID == relative[len(relative)-1] {
			fileSessionID = ""
		}
	}
	if len(relative) == 2 && fileSessionID != "" {
		return fileSessionID, relative[0]
	}
	if len(relative) >= 4 && relative[len(relative)-2] == "subagents" {
		sessionID := relative[len(relative)-3]
		projectPath := strings.Join(relative[:len(relative)-3], string(filepath.Separator))
		if projectPath == "" {
			projectPath = "Unknown Project"
		}
		return sessionID, projectPath
	}

	sessionID := "unknown"
	if len(relative) >= 2 {
		sessionID = relative[len(relative)-2]
	}
	projectPath := "Unknown Project"
	if len(relative) > 2 {
		projectPath = strings.Join(relative[:len(relative)-2], string(filepath.Separator))
	}
	return sessionID, projectPath
}

func parseUsageLine(line []byte, project, sessionID, projectPath string) (LoadedEntry, bool) {
	if !bytes.Contains(line, []byte(`"usage":{`)) {
		return LoadedEntry{}, false
	}
	if hasUnsupportedNullField(line) {
		return LoadedEntry{}, false
	}

	var data UsageEntry
	if err := json.Unmarshal(line, &data); err != nil {
		return LoadedEntry{}, false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, data.Timestamp)
	if err != nil {
		return LoadedEntry{}, false
	}
	if !isValidUsageEntry(data) {
		return LoadedEntry{}, false
	}

	model := ""
	if data.Message.Model != nil && *data.Message.Model != "<synthetic>" {
		model = *data.Message.Model
		if data.Message.Usage.Speed != nil && *data.Message.Usage.Speed == SpeedFast {
			model += "-fast"
		}
	}

	client := ""
	if data.Entrypoint != nil {
		client = usage.NormalizeClient(usage.ProviderClaude, *data.Entrypoint)
	}

	return LoadedEntry{
		Data:                data,
		Timestamp:           timestamp,
		Date:                timestamp.In(time.Local).Format("2006-01-02"),
		Project:             project,
		SessionID:           sessionID,
		ProjectPath:         projectPath,
		Model:               model,
		Client:              client,
		UsageLimitResetTime: usageLimitResetTimeFromLine(line, data.IsAPIErrorMessage),
	}, true
}

func stableEntryID(entry LoadedEntry) string {
	tokens := entry.Data.Message.Usage
	requestID := ""
	if entry.Data.RequestID != nil {
		requestID = *entry.Data.RequestID
	}
	messageID := ""
	if entry.Data.Message.ID != nil {
		messageID = *entry.Data.Message.ID
	}
	if requestID != "" || messageID != "" {
		return usage.StableID(
			string(usage.ProviderClaude),
			entry.SessionID,
			requestID,
			messageID,
			entry.Data.Timestamp,
			entry.Model,
			strconv.FormatUint(tokens.InputTokens, 10),
			strconv.FormatUint(tokens.OutputTokens, 10),
			strconv.FormatUint(tokens.CacheCreationInputTokens, 10),
			strconv.FormatUint(tokens.CacheReadInputTokens, 10),
		)
	}
	return usage.StableID(
		string(usage.ProviderClaude),
		entry.SourceFile,
		strconv.Itoa(entry.SourceLine),
		entry.Data.Timestamp,
		entry.Model,
		strconv.FormatUint(tokens.InputTokens, 10),
		strconv.FormatUint(tokens.OutputTokens, 10),
		strconv.FormatUint(tokens.CacheCreationInputTokens, 10),
		strconv.FormatUint(tokens.CacheReadInputTokens, 10),
	)
}

func isValidUsageEntry(data UsageEntry) bool {
	if data.Version != nil && !isSemverPrefix(*data.Version) {
		return false
	}
	if data.SessionID != nil && *data.SessionID == "" {
		return false
	}
	if data.RequestID != nil && *data.RequestID == "" {
		return false
	}
	if data.Message.ID != nil && *data.Message.ID == "" {
		return false
	}
	if data.Message.Model != nil && *data.Message.Model == "" {
		return false
	}
	return true
}

func hasUnsupportedNullField(line []byte) bool {
	offset := 0
	for {
		relativeIndex := bytes.Index(line[offset:], []byte(":null"))
		if relativeIndex < 0 {
			return false
		}
		nullIndex := offset + relativeIndex
		fieldEnd := nullIndex - 1
		if fieldEnd < 0 {
			return false
		}
		if line[fieldEnd] != '"' {
			for fieldEnd > 0 && line[fieldEnd] != '"' {
				fieldEnd--
			}
		}
		if line[fieldEnd] == '"' {
			fieldStart := fieldEnd - 1
			for fieldStart > 0 && line[fieldStart] != '"' {
				fieldStart--
			}
			if line[fieldStart] == '"' && isUnsupportedNullableField(string(line[fieldStart+1:fieldEnd])) {
				return true
			}
		}
		offset = nullIndex + len(":null")
	}
}

func isUnsupportedNullableField(field string) bool {
	switch field {
	case "id",
		"cwd",
		"model",
		"speed",
		"costUSD",
		"version",
		"sessionId",
		"requestId",
		"isApiErrorMessage",
		"cache_read_input_tokens",
		"cache_creation_input_tokens":
		return true
	default:
		return false
	}
}

func isSemverPrefix(value string) bool {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) < 3 {
		return false
	}
	for _, part := range parts[:2] {
		if part == "" || !allDigits(part) {
			return false
		}
	}
	return parts[2] != "" && parts[2][0] >= '0' && parts[2][0] <= '9'
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func usageLimitResetTimeFromLine(line []byte, isAPIErrorMessage *bool) *time.Time {
	if isAPIErrorMessage == nil || !*isAPIErrorMessage {
		return nil
	}
	marker := []byte("Claude AI usage limit reached")
	markerStart := bytes.Index(line, marker)
	if markerStart < 0 {
		return nil
	}
	afterMarker := line[markerStart:]
	pipeIndex := bytes.IndexByte(afterMarker, '|')
	if pipeIndex < 0 {
		return nil
	}
	start := markerStart + pipeIndex + 1
	end := start
	for end < len(line) && line[end] >= '0' && line[end] <= '9' {
		end++
	}
	if start == end {
		return nil
	}
	seconds, err := strconv.ParseInt(string(line[start:end]), 10, 64)
	if err != nil || seconds <= 0 {
		return nil
	}
	timestamp := time.Unix(seconds, 0).UTC()
	return &timestamp
}

func appendDeduped(entries []LoadedEntry, candidate LoadedEntry) []LoadedEntry {
	if candidate.Data.Message.ID == nil {
		return append(entries, candidate)
	}
	for i := range entries {
		if sameDedupeKey(entries[i], candidate) {
			if shouldReplaceDedupedEntry(candidate, entries[i]) {
				entries[i] = candidate
			}
			return entries
		}
	}
	return append(entries, candidate)
}

func sameDedupeKey(a, b LoadedEntry) bool {
	if a.Data.Message.ID == nil || b.Data.Message.ID == nil {
		return false
	}
	if *a.Data.Message.ID != *b.Data.Message.ID {
		return false
	}
	if a.Data.RequestID == nil || b.Data.RequestID == nil {
		return a.Data.RequestID == b.Data.RequestID
	}
	return *a.Data.RequestID == *b.Data.RequestID
}

func shouldReplaceDedupedEntry(candidate, existing LoadedEntry) bool {
	candidateTotal := tokenTotal(candidate.Data.Message.Usage)
	existingTotal := tokenTotal(existing.Data.Message.Usage)
	if candidateTotal != existingTotal {
		return candidateTotal > existingTotal
	}
	return candidate.Data.Message.Usage.Speed != nil && existing.Data.Message.Usage.Speed == nil
}

func tokenTotal(usage TokenUsage) uint64 {
	return usage.InputTokens +
		usage.OutputTokens +
		usage.CacheCreationInputTokens +
		usage.CacheReadInputTokens
}

func collectUsageFiles(dir string, files *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			collectUsageFiles(path, files)
			continue
		}
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			*files = append(*files, path)
		}
	}
}

func isProjectPathSegment(value string) bool {
	return value != "" &&
		value != "." &&
		value != ".." &&
		!strings.Contains(value, "/") &&
		!strings.Contains(value, `\`)
}

func normalizeClaudeConfigPath(raw string) string {
	path := expandHomePath(raw)
	if filepath.Base(path) == "projects" && dirExists(path) {
		return filepath.Dir(path)
	}
	return path
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func pathParts(path string) []string {
	clean := filepath.Clean(path)
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return parts
}
