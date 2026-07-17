package agentusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const maxInt64Uint = uint64(1<<63 - 1)

func collectFiles(root string, match func(string) bool) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if match(root) {
			return []string{root}
		}
		return nil
	}

	files := make([]string, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if match(path) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func collectExt(root, ext string) []string {
	ext = strings.ToLower(ext)
	return collectFiles(root, func(path string) bool {
		return strings.EqualFold(filepath.Ext(path), ext)
	})
}

func readJSONLines(path string, prefilter ...string) ([]lineJSON, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := make([]lineJSON, 0)
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
			if matchesPrefilter(line, prefilter) {
				var value map[string]any
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err == nil {
					lines = append(lines, lineJSON{
						value: value,
						line:  lineNumber,
						start: start,
						end:   offset,
					})
				}
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
	return lines, nil
}

type lineJSON struct {
	value map[string]any
	line  int
	start int64
	end   int64
}

func matchesPrefilter(line []byte, filters []string) bool {
	for _, filter := range filters {
		if filter != "" && !bytes.Contains(line, []byte(filter)) {
			return false
		}
	}
	return true
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, nil
	}
	return value, nil
}

func objectAt(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func arrayAt(value any) []any {
	array, _ := value.([]any)
	return array
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func stringField(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	return stringValue(object[key])
}

func firstStringField(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringField(object, key); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uintValue(value any) uint64 {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := strconv.ParseUint(typed.String(), 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return floatToUint(parsed)
		}
	case float64:
		return floatToUint(typed)
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case uint64:
		return typed
	case string:
		if parsed, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return floatToUint(parsed)
		}
	}
	return 0
}

func floatToUint(value float64) uint64 {
	if !isFinite(value) || value <= 0 {
		return 0
	}
	if value > float64(^uint64(0)) {
		return ^uint64(0)
	}
	return uint64(math.Trunc(value))
}

func uintField(object map[string]any, keys ...string) uint64 {
	for _, key := range keys {
		if value := uintValue(object[key]); value > 0 {
			return value
		}
	}
	return 0
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		return parsed, err == nil && isFinite(parsed)
	case float64:
		return typed, isFinite(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil && isFinite(parsed)
	default:
		return 0, false
	}
}

func parseTimestamp(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		return parseTimestampString(typed)
	case json.Number:
		if integer, err := strconv.ParseUint(typed.String(), 10, 64); err == nil {
			return timestampFromScalar(integer)
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return timestampFromFloat(parsed)
		}
	case float64:
		return timestampFromFloat(typed)
	}
	return time.Time{}, false
}

func parseTimestampString(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, true
	}
	if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return timestampFromScalar(parsed)
	}
	if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
		return timestampFromFloat(parsed)
	}
	return time.Time{}, false
}

func timestampFromFloat(value float64) (time.Time, bool) {
	if !isFinite(value) || value <= 0 {
		return time.Time{}, false
	}
	if value < 100_000_000_000 {
		return time.UnixMilli(int64(value * 1000)), true
	}
	return time.UnixMilli(int64(value)), true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func timestampFromScalar(raw uint64) (time.Time, bool) {
	if raw == 0 {
		return time.Time{}, false
	}
	var millis uint64
	switch {
	case raw >= 100_000_000_000_000_000:
		millis = raw / 1_000_000
	case raw >= 100_000_000_000_000:
		millis = raw / 1_000
	case raw >= 100_000_000_000:
		millis = raw
	default:
		millis = raw * 1_000
	}
	if millis > maxInt64Uint {
		millis = maxInt64Uint
	}
	return time.UnixMilli(int64(millis)), true
}

func timestampFromParts(value any) (time.Time, bool) {
	parts := arrayAt(value)
	if len(parts) < 2 {
		return time.Time{}, false
	}
	seconds := uintValue(parts[0])
	nanos := uintValue(parts[1])
	if seconds == 0 {
		return time.Time{}, false
	}
	millis := seconds*1000 + nanos/1_000_000
	if millis > maxInt64Uint {
		millis = maxInt64Uint
	}
	return time.UnixMilli(int64(millis)), true
}

func fileModifiedTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	modified := info.ModTime()
	if modified.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return modified
}

func formatDate(timestamp time.Time) string {
	return timestamp.In(time.Local).Format("2006-01-02")
}

func totalUsage(tokens usage.TokenUsage) uint64 {
	return tokens.InputTokens +
		tokens.OutputTokens +
		tokens.CacheCreationInputTokens +
		tokens.CacheReadInputTokens +
		tokens.CachedInputTokens +
		tokens.ReasoningOutputTokens
}

func applyTotalFallback(tokens usage.TokenUsage, total uint64) usage.TokenUsage {
	sum := totalUsage(tokens)
	if sum == 0 && total > 0 {
		tokens.OutputTokens = total
		tokens.TotalTokens = total
		return tokens
	}
	if total > sum {
		tokens.ReasoningOutputTokens += total - sum
		tokens.TotalTokens = total
		return tokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = sum
	}
	return tokens
}

func nonZero(tokens usage.TokenUsage) bool {
	return totalUsage(tokens) > 0 || tokens.TotalTokens > 0
}

func baseEntry(provider usage.Provider, timestamp time.Time, project, projectPath, sessionID, model, client string, tokens usage.TokenUsage) usage.Entry {
	return usage.Entry{
		Provider:    provider,
		Timestamp:   timestamp,
		Date:        formatDate(timestamp),
		Project:     project,
		ProjectPath: projectPath,
		SessionID:   sessionID,
		Model:       model,
		Language:    usage.UnknownLanguage,
		OS:          usage.NormalizeOS(runtime.GOOS),
		Client:      client,
		Usage:       tokens,
	}
}

func setSource(entry *usage.Entry, source string, line int, start, end int64) {
	entry.SourceFile = source
	entry.SourceLine = line
	entry.SourceStart = start
	entry.SourceEnd = end
}

func stableEntryID(entry usage.Entry, extra ...string) string {
	parts := []string{
		string(entry.Provider),
		entry.SourceFile,
		strconv.Itoa(entry.SourceLine),
		entry.Timestamp.Format(time.RFC3339Nano),
		entry.Project,
		entry.ProjectPath,
		entry.SessionID,
		entry.Model,
		strconv.FormatUint(entry.Usage.InputTokens, 10),
		strconv.FormatUint(entry.Usage.OutputTokens, 10),
		strconv.FormatUint(entry.Usage.CacheCreationInputTokens, 10),
		strconv.FormatUint(entry.Usage.CacheReadInputTokens, 10),
		strconv.FormatUint(entry.Usage.CachedInputTokens, 10),
		strconv.FormatUint(entry.Usage.ReasoningOutputTokens, 10),
		strconv.FormatUint(entry.Usage.TotalTokens, 10),
	}
	parts = append(parts, extra...)
	return usage.StableID(parts...)
}

func sortEntries(entries []usage.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].Timestamp.Before(entries[j].Timestamp)
		}
		return entries[i].ID < entries[j].ID
	})
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if value == "" {
			continue
		}
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
