// Package agentdata reads the JSON, JSONL and on-disk layouts that local AI
// agents write their logs in.
package agentdata

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const maxInt64Uint = uint64(1<<63 - 1)

func CollectFiles(root string, match func(string) bool) []string {
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

// FilterFiles drops files the filter rejects. A nil filter keeps everything.
func FilterFiles(files []string, filter usage.FileFilter) []string {
	if filter == nil {
		return files
	}
	kept := files[:0]
	for _, file := range files {
		if filter(file) {
			kept = append(kept, file)
		}
	}
	return kept
}

func CollectExt(root, ext string) []string {
	ext = strings.ToLower(ext)
	return CollectFiles(root, func(path string) bool {
		return strings.EqualFold(filepath.Ext(path), ext)
	})
}

func ReadJSONLines(path string, prefilter ...string) ([]LineJSON, error) {
	lines, _, err := ReadJSONLinesFrom(path, 0, prefilter...)
	return lines, err
}

// ReadJSONLinesFrom parses a JSONL file starting at byte offset start and
// reports the offset to resume from next time.
//
// This exists for transcripts that are appended to while being read: an
// active session's file grows continuously, and re-reading megabytes of
// history to pick up the newest few lines is the cost resuming avoids.
//
// The returned offset is the end of the last line that arrived with its
// newline, never the end of the file. A trailing line without one is either
// the last line of a finished file or the front of one still being written,
// and the two are indistinguishable from here. It is parsed either way, so a
// file that merely lacks a final newline is not ignored, but the resume point
// stops before it: if more of it arrives later, the next pass re-reads the
// whole line. Re-reading one line costs nothing; skipping a real one loses it.
//
// The offset also advances past lines that fail to parse or that the
// prefilter rejects. Those are lines the file has moved beyond; stopping
// there would turn one malformed record into a permanent roadblock hiding
// everything after it.
//
// Only callers whose files are append-only may resume. A caller that reads a
// document rewritten in place, or that needs cross-line context from earlier
// in the file, must keep using ReadJSONLines.
func ReadJSONLinesFrom(path string, start int64, prefilter ...string) ([]LineJSON, int64, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	if start > 0 {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return nil, 0, err
		}
	}

	lines := make([]LineJSON, 0)
	reader := bufio.NewReader(file)
	lineNumber := 0
	offset := start
	consumed := start
	for {
		line, readErr := reader.ReadBytes('\n')
		complete := readErr == nil
		if len(line) > 0 {
			lineNumber++
			lineStart := offset
			offset += int64(len(line))
			if complete {
				consumed = offset
			}
			line = bytes.TrimRight(line, "\r\n")
			if matchesPrefilter(line, prefilter) {
				var value map[string]any
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err == nil {
					lines = append(lines, LineJSON{
						Value: value,
						Line:  lineNumber,
						Start: lineStart,
						End:   offset,
					})
				}
			}
		}
		if complete {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return nil, 0, readErr
	}
	return lines, consumed, nil
}

type LineJSON struct {
	Value map[string]any
	Line  int
	Start int64
	End   int64
}

func matchesPrefilter(line []byte, filters []string) bool {
	for _, filter := range filters {
		if filter != "" && !bytes.Contains(line, []byte(filter)) {
			return false
		}
	}
	return true
}

func ReadJSONObject(path string) (map[string]any, error) {
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

func ObjectAt(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func ArrayAt(value any) []any {
	array, _ := value.([]any)
	return array
}

func StringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func StringField(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	return StringValue(object[key])
}

func FirstStringField(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := StringField(object, key); value != "" {
			return value
		}
	}
	return ""
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func UintValue(value any) uint64 {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := strconv.ParseUint(typed.String(), 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return FloatToUint(parsed)
		}
	case float64:
		return FloatToUint(typed)
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
			return FloatToUint(parsed)
		}
	}
	return 0
}

func FloatToUint(value float64) uint64 {
	if !IsFinite(value) || value <= 0 {
		return 0
	}
	if value > float64(^uint64(0)) {
		return ^uint64(0)
	}
	return uint64(math.Trunc(value))
}

func UintField(object map[string]any, keys ...string) uint64 {
	for _, key := range keys {
		if value := UintValue(object[key]); value > 0 {
			return value
		}
	}
	return 0
}

func ParseTimestamp(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		return ParseTimestampString(typed)
	case json.Number:
		if integer, err := strconv.ParseUint(typed.String(), 10, 64); err == nil {
			return timestampFromScalar(integer)
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return TimestampFromFloat(parsed)
		}
	case float64:
		return TimestampFromFloat(typed)
	}
	return time.Time{}, false
}

func ParseTimestampString(raw string) (time.Time, bool) {
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
		return TimestampFromFloat(parsed)
	}
	return time.Time{}, false
}

func TimestampFromFloat(value float64) (time.Time, bool) {
	if !IsFinite(value) || value <= 0 {
		return time.Time{}, false
	}
	if value < 100_000_000_000 {
		return time.UnixMilli(int64(value * 1000)), true
	}
	return time.UnixMilli(int64(value)), true
}

func IsFinite(value float64) bool {
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

func TimestampFromParts(value any) (time.Time, bool) {
	parts := ArrayAt(value)
	if len(parts) < 2 {
		return time.Time{}, false
	}
	seconds := UintValue(parts[0])
	nanos := UintValue(parts[1])
	if seconds == 0 {
		return time.Time{}, false
	}
	millis := seconds*1000 + nanos/1_000_000
	if millis > maxInt64Uint {
		millis = maxInt64Uint
	}
	return time.UnixMilli(int64(millis)), true
}

func FileModifiedTime(path string) time.Time {
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

func UniqueStrings(values []string) []string {
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

func DecodeJSONObjectString(data string) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil
	}
	return record
}
