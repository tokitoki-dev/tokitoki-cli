package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

var ErrNoDataDirs = errors.New("no valid Claude data directories found")

type UsageEntry struct {
	SessionID         *string      `json:"sessionId"`
	Timestamp         string       `json:"timestamp"`
	Version           *string      `json:"version"`
	Entrypoint        *string      `json:"entrypoint"`
	CWD               *string      `json:"cwd"`
	GitBranch         *string      `json:"gitBranch"`
	Message           UsageMessage `json:"message"`
	CostUSD           *float64     `json:"costUSD"`
	RequestID         *string      `json:"requestId"`
	IsAPIErrorMessage *bool        `json:"isApiErrorMessage"`
}

type UsageMessage struct {
	Usage   TokenUsage      `json:"usage"`
	Model   *string         `json:"model"`
	ID      *string         `json:"id"`
	Content json.RawMessage `json:"content"`
}

type TokenUsage struct {
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
	// CacheCreation is the TTL breakdown of CacheCreationInputTokens.
	// The 1h tier bills at 2.0x input vs 1.25x for 5m, so dropping this
	// object silently underprices every 1h cache write by 37.5%.
	CacheCreation *CacheCreationBreakdown `json:"cache_creation"`
	Speed         *Speed                  `json:"speed"`
}

// CacheCreationBreakdown mirrors the usage.cache_creation object in Claude
// Code transcripts: cache_creation_input_tokens split by ephemeral TTL.
type CacheCreationBreakdown struct {
	Ephemeral5mInputTokens uint64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens uint64 `json:"ephemeral_1h_input_tokens"`
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

// LoadedEntry is one event parsed from one transcript line. Kind says which
// of the two the line produced:
//
//   - usage.EventKindAPICall: an assistant line carrying usage. One API round
//     trip. Claude Code writes such a message as several lines (one per
//     content block) that share message.id, requestId and usage; each line
//     yields an entry with the same ID and the same content, and the store
//     keeps one.
//   - usage.EventKindFileEdit: a tool_result line carrying a structuredPatch
//     (or a create). One file modification, keyed on the tool_use id the
//     result answers.
//
// No entry depends on any other line. A transcript can be parsed from any
// line boundary and produce the same entries for the lines it covers, which
// is what makes resuming at a byte offset exact rather than approximate.
type LoadedEntry struct {
	Kind                string             `json:"kind"`
	Data                UsageEntry         `json:"data"`
	ID                  string             `json:"id,omitempty"`
	SourceFile          string             `json:"source_file,omitempty"`
	SourceLine          int                `json:"source_line,omitempty"`
	SourceStart         int64              `json:"source_start,omitempty"`
	SourceEnd           int64              `json:"source_end,omitempty"`
	Timestamp           time.Time          `json:"timestamp"`
	Date                string             `json:"date"`
	Project             string             `json:"project"`
	SessionID           string             `json:"session_id"`
	ProjectPath         string             `json:"project_path"`
	Model               string             `json:"model,omitempty"`
	Language            string             `json:"language"`
	Client              string             `json:"client,omitempty"`
	Branch              string             `json:"branch,omitempty"`
	Entity              string             `json:"entity,omitempty"`
	IsWrite             bool               `json:"is_write,omitempty"`
	LinesAdded          uint64             `json:"lines_added,omitempty"`
	LinesRemoved        uint64             `json:"lines_removed,omitempty"`
	Files               []usage.FileChange `json:"files,omitempty"`
	ToolUseID           string             `json:"tool_use_id,omitempty"`
	UsageLimitResetTime *time.Time         `json:"usage_limit_reset_time,omitempty"`
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
		var isWrite *bool
		if entry.IsWrite {
			t := true
			isWrite = &t
		}
		entityType := ""
		if entry.Entity != "" {
			entityType = "file"
		}
		var raw map[string]any
		if entry.ToolUseID != "" {
			raw = map[string]any{"tool_use_id": entry.ToolUseID}
		}
		converted = append(converted, usage.Entry{
			Provider:     usage.ProviderClaude,
			ID:           entry.ID,
			EventKind:    entry.Kind,
			SourceFile:   entry.SourceFile,
			SourceLine:   entry.SourceLine,
			SourceStart:  entry.SourceStart,
			SourceEnd:    entry.SourceEnd,
			Timestamp:    entry.Timestamp,
			Date:         entry.Date,
			Project:      entry.Project,
			ProjectPath:  entry.ProjectPath,
			SessionID:    entry.SessionID,
			Model:        entry.Model,
			Language:     usage.NormalizeLanguage(entry.Language),
			OS:           usage.NormalizeOS(runtime.GOOS),
			Client:       entry.Client,
			Branch:       entry.Branch,
			Entity:       entry.Entity,
			EntityType:   entityType,
			IsWrite:      isWrite,
			LinesAdded:   entry.LinesAdded,
			LinesRemoved: entry.LinesRemoved,
			Files:        entry.Files,
			Raw:          raw,
			Usage: usage.TokenUsage{
				InputTokens:                tokens.InputTokens,
				OutputTokens:               tokens.OutputTokens,
				CacheCreationInputTokens:   tokens.CacheCreationInputTokens,
				CacheCreation5mInputTokens: cacheCreation5m(tokens),
				CacheCreation1hInputTokens: cacheCreation1h(tokens),
				CacheReadInputTokens:       tokens.CacheReadInputTokens,
				TotalTokens:                tokenTotal(tokens),
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

// LoadEntriesFromPaths reads every transcript under paths into one list with
// no two entries sharing an ID. The first occurrence wins, which is also what
// the store does on insert; duplicates carry identical content, so which copy
// survives makes no difference.
func LoadEntriesFromPaths(paths []string, projectFilter string, fileFilter usage.FileFilter) ([]LoadedEntry, error) {
	files := UsageFiles(paths, projectFilter)
	entries := make([]LoadedEntry, 0)
	seen := make(map[string]bool)
	for _, file := range files {
		if fileFilter != nil && !fileFilter(file) {
			continue
		}
		fileEntries, err := ReadUsageFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if projectFilter != "" && entry.Project != projectFilter {
				continue
			}
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// UsageFiles lists the transcripts under paths, most recently modified first,
// so a scan reaches the sessions a user is working in now before it reaches
// their history. Order never affects what is parsed — every entry comes from
// one line — only what is queued first.
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
	modified := make(map[string]int64, len(files))
	for _, file := range files {
		if info, err := os.Stat(file); err == nil {
			modified[file] = info.ModTime().UnixNano()
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if modified[files[i]] != modified[files[j]] {
			return modified[files[i]] > modified[files[j]]
		}
		return files[i] < files[j]
	})
	return files
}

func ReadUsageFile(path string) ([]LoadedEntry, error) {
	entries, _, err := ReadUsageFileFrom(path, 0)
	return entries, err
}

// ReadUsageFileFrom parses a transcript starting at byte offset start and
// reports the offset to resume from next time.
//
// Transcripts are append-only and are read while Claude is still writing to
// them, so the returned offset is the end of the last line that arrived with
// its newline — never the end of the file. A trailing partial line is left
// unconsumed for the next pass, when the rest of it exists.
//
// The offset advances past lines that fail to parse. A line the parser cannot
// use is still a line the file has moved beyond; stopping there would turn one
// malformed record into a permanent roadblock hiding everything after it.
//
// Each line is parsed on its own: parseLine is a pure function of its bytes,
// so resuming at any line boundary yields the same entries, with the same
// ids, that a whole read would yield for the lines after it.
//
// Language is the one field that is not a property of a single line, and it
// is resolved here rather than there. See sessionLanguage.
func ReadUsageFileFrom(path string, start int64) ([]LoadedEntry, int64, error) {
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

	sessionID := ExtractSessionID(path)
	entries := make([]LoadedEntry, 0)
	language := newSessionLanguage(path, start)
	reader := bufio.NewReader(file)
	lineNumber := 0
	offset := start
	consumed := start
	for {
		line, readErr := reader.ReadBytes('\n')
		// A line that arrived without its newline is either the last line of
		// a finished file or the front of one still being written, and the
		// two are indistinguishable from here. It is parsed either way, so a
		// file that simply lacks a trailing newline is not ignored, but the
		// resume point stops short of it: if more of it arrives later, the
		// next pass re-reads the whole line and supersedes what this one
		// produced. Re-reading one line costs nothing; skipping a real one
		// loses it permanently.
		complete := readErr == nil
		if len(line) > 0 {
			lineNumber++
			lineStart := offset
			offset += int64(len(line))
			if complete {
				consumed = offset
			}
			line = bytes.TrimRight(line, "\r\n")
			if entry, ok := parseLine(line, sessionID); ok {
				entry.SourceFile = path
				entry.SourceLine = lineNumber
				entry.SourceStart = lineStart
				entry.SourceEnd = offset
				entry.Language = language.resolve(entry.Language)
				entries = append(entries, entry)
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
	return entries, consumed, nil
}

// sessionLanguage carries the language a session is working in across the
// lines of one transcript.
//
// A language is a property of the session, not of the message that happens to
// mention a file. An assistant turn is either a tool call, whose input names
// the file being read or written, or prose — an explanation, a question, a
// plan — that names nothing. Judging each line alone, as this parser used to,
// files every prose turn under Unknown: in this repository's own transcripts
// that is 51 of 56 such turns, and prose turns carry the larger token counts,
// so "Unknown" became the top language by tokens while the work was plainly
// TypeScript. Codex never had the bug because its loader already threaded a
// per-session language through its scan (provider/codex/loader.go).
//
// The fix is to remember the last language actually observed and lend it to
// the turns that name nothing. It is the same session, the same files, one
// turn apart.
//
// Resume is the constraint that shapes this. Claude transcripts are read
// incrementally from a stored byte offset, so a scan can start in the middle
// of a session whose earlier lines named every file. Sticky state built only
// from lines this pass happens to read would make an entry's language depend
// on where the previous scan stopped — the same line yielding TypeScript on a
// full read and Unknown on a resumed one, with no way to tell which rows in
// the database came from which. So a resumed scan seeds itself from the
// prefix it is skipping, and reads exactly what a full read would have known
// at that point. The seek is bounded work over an already-open file, and only
// on the first parsed line of a resumed pass.
//
// The language still only ever moves forward to a language that was actually
// seen; nothing is invented for a session that never named a file.
type sessionLanguage struct {
	path    string
	start   int64
	seeded  bool
	current string
}

func newSessionLanguage(path string, start int64) *sessionLanguage {
	return &sessionLanguage{path: path, start: start}
}

// resolve records a detected language and fills in an undetected one.
func (s *sessionLanguage) resolve(detected string) string {
	if detected != "" && detected != langdetect.Unknown {
		s.current = detected
		s.seeded = true
		return detected
	}
	if !s.seeded {
		s.current = languageBefore(s.path, s.start)
		s.seeded = true
	}
	if s.current == "" {
		return langdetect.Unknown
	}
	return s.current
}

// languageBefore reports the last language named in path before offset, so a
// resumed scan inherits what the lines it skipped already established.
//
// Returns "" for a scan starting at the beginning of a file — there is no
// prefix — and for any read failure: an unreadable prefix means the language
// is unknown, which is exactly what the entry would have said anyway. A
// transcript is scanned in order and the last match wins, matching what a
// full read would have been holding when it reached this point.
func languageBefore(path string, offset int64) string {
	if offset <= 0 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	language := ""
	reader := bufio.NewReader(io.LimitReader(file, offset))
	sessionID := ExtractSessionID(path)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if entry, ok := parseLine(line, sessionID); ok {
				if entry.Language != "" && entry.Language != langdetect.Unknown {
					language = entry.Language
				}
			}
		}
		if readErr != nil {
			return language
		}
	}
}

// parseLine turns one transcript line into at most one entry. It is a pure
// function of the line: the same bytes always yield the same entry with the
// same ID, whichever file or pass they are read in.
//
// A panic while decoding — a shape this code never anticipated — is contained
// to the line. Transcript formats change under us; one strange record must
// not take down the scan of every file after it.
func parseLine(line []byte, sessionID string) (entry LoadedEntry, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Debug("claude transcript line skipped after panic", "error", r)
			entry, ok = LoadedEntry{}, false
		}
	}()

	// Cheap byte checks first: the vast majority of lines are prompts, tool
	// output and bookkeeping that carry neither usage nor a diff.
	mayBeEdit := bytes.Contains(line, []byte(`"toolUseResult"`)) &&
		(bytes.Contains(line, []byte(`"structuredPatch"`)) || bytes.Contains(line, []byte(`"type":"create"`)))
	mayBeCall := bytes.Contains(line, []byte(`"usage":{`))
	if !mayBeEdit && !mayBeCall {
		return LoadedEntry{}, false
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return LoadedEntry{}, false
	}
	data := decodeEnvelope(raw)
	timestamp, err := time.Parse(time.RFC3339Nano, data.Timestamp)
	if err != nil {
		return LoadedEntry{}, false
	}
	if !isValidEnvelope(data) {
		return LoadedEntry{}, false
	}
	base := baseEntry(data, timestamp, sessionID)

	if mayBeEdit {
		if entry, ok := parseEditLine(raw, base); ok {
			return entry, true
		}
	}
	if mayBeCall {
		return parseCallLine(raw, line, base)
	}
	return LoadedEntry{}, false
}

// decodeEnvelope reads the line-level fields one at a time. Claude Code's
// transcript schema changes often; a field that has taken on a new shape must
// not discard the rest of a line that is otherwise perfectly usable.
func decodeEnvelope(raw map[string]json.RawMessage) UsageEntry {
	var data UsageEntry
	decodeField(raw, "sessionId", &data.SessionID)
	decodeField(raw, "timestamp", &data.Timestamp)
	decodeField(raw, "version", &data.Version)
	decodeField(raw, "entrypoint", &data.Entrypoint)
	decodeField(raw, "cwd", &data.CWD)
	decodeField(raw, "gitBranch", &data.GitBranch)
	decodeField(raw, "requestId", &data.RequestID)
	decodeField(raw, "costUSD", &data.CostUSD)
	decodeField(raw, "isApiErrorMessage", &data.IsAPIErrorMessage)
	return data
}

// decodeField decodes one optional key into value, reporting whether the key
// was present and decoded. A key that is absent or null leaves value alone.
func decodeField(raw map[string]json.RawMessage, key string, value any) bool {
	data, ok := raw[key]
	if !ok || bytes.Equal(data, []byte("null")) {
		return false
	}
	return json.Unmarshal(data, value) == nil
}

func isValidEnvelope(data UsageEntry) bool {
	if data.Version != nil && !isSemverPrefix(*data.Version) {
		return false
	}
	if data.SessionID != nil && *data.SessionID == "" {
		return false
	}
	if data.RequestID != nil && *data.RequestID == "" {
		return false
	}
	return true
}

func baseEntry(data UsageEntry, timestamp time.Time, sessionID string) LoadedEntry {
	// The transcript line's cwd is the only trustworthy project source: the
	// directory name under ~/.claude/projects encodes "/", "-", "_" and "."
	// identically, so decoding it is guesswork. No cwd means no project.
	project := usage.UnknownProject
	projectPath := ""
	if data.CWD != nil {
		if path, name, ok := usage.ProjectFromCWD(*data.CWD); ok {
			projectPath = path
			project = name
		}
	}
	client := ""
	if data.Entrypoint != nil {
		client = usage.NormalizeClient(*data.Entrypoint)
	}
	branch := ""
	if data.GitBranch != nil {
		branch = strings.TrimSpace(*data.GitBranch)
	}
	return LoadedEntry{
		Data:        data,
		Timestamp:   timestamp,
		Date:        timestamp.In(time.Local).Format("2006-01-02"),
		Project:     project,
		SessionID:   sessionID,
		ProjectPath: projectPath,
		Client:      client,
		Branch:      branch,
	}
}

// parseCallLine builds the api_call entry for an assistant line.
func parseCallLine(raw map[string]json.RawMessage, line []byte, entry LoadedEntry) (LoadedEntry, bool) {
	var message map[string]json.RawMessage
	if !decodeField(raw, "message", &message) {
		return LoadedEntry{}, false
	}
	msg := &entry.Data.Message
	decodeField(message, "id", &msg.ID)
	decodeField(message, "model", &msg.Model)
	decodeField(message, "content", &msg.Content)
	// Usage is the payload. A usage block this code cannot read is not
	// something to approximate — the line is skipped rather than billed wrong.
	usageRaw, ok := message["usage"]
	if !ok {
		return LoadedEntry{}, false
	}
	if err := json.Unmarshal(usageRaw, &msg.Usage); err != nil {
		return LoadedEntry{}, false
	}

	// A message id is what identifies the event. Without one there is nothing
	// to deduplicate on, and the same message replayed across session files
	// would be counted once per copy.
	if msg.ID == nil || *msg.ID == "" {
		return LoadedEntry{}, false
	}
	if msg.Model != nil && *msg.Model == "" {
		return LoadedEntry{}, false
	}
	// Nothing was billed: synthetic messages, API error echoes. Not a call.
	if tokenTotal(msg.Usage) == 0 {
		return LoadedEntry{}, false
	}

	model := ""
	if msg.Model != nil && *msg.Model != "<synthetic>" {
		model = *msg.Model
		if msg.Usage.Speed != nil && *msg.Usage.Speed == SpeedFast {
			model += "-fast"
		}
	}

	entry.Kind = usage.EventKindAPICall
	entry.ID = stableEntryID(entry)
	entry.Model = model
	entry.Language = languageFromContent(msg.Content)
	entry.UsageLimitResetTime = usageLimitResetTimeFromLine(line, entry.Data.IsAPIErrorMessage)
	return entry, true
}

// parseEditLine builds the file_edit entry for a tool_result line whose
// toolUseResult records a diff.
func parseEditLine(raw map[string]json.RawMessage, entry LoadedEntry) (LoadedEntry, bool) {
	var result struct {
		Type            string `json:"type"`
		FilePath        string `json:"filePath"`
		Content         string `json:"content"`
		StructuredPatch []struct {
			Lines []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if !decodeField(raw, "toolUseResult", &result) || result.FilePath == "" {
		return LoadedEntry{}, false
	}

	var added, removed uint64
	switch {
	case len(result.StructuredPatch) > 0:
		for _, hunk := range result.StructuredPatch {
			for _, hunkLine := range hunk.Lines {
				if len(hunkLine) == 0 {
					continue
				}
				switch hunkLine[0] {
				case '+':
					added++
				case '-':
					removed++
				}
			}
		}
	case result.Type == "create":
		// Creating a file records no diff hunks, only the full content:
		// every content line is an added line.
		added = usage.CountLines(result.Content)
	default:
		return LoadedEntry{}, false
	}

	// The tool_use id is the edit's identity: the API assigns it once per
	// tool call, and Claude Code preserves it when it copies a session's
	// history into a forked one. No id, no identity, no event.
	toolUseID := toolUseIDFromResult(raw)
	if toolUseID == "" {
		slog.Debug("claude file edit skipped: tool_result has no tool_use_id", "file", result.FilePath)
		return LoadedEntry{}, false
	}

	entry.Kind = usage.EventKindFileEdit
	entry.ID = usage.StableID(string(usage.ProviderClaude), "edit", toolUseID)
	entry.ToolUseID = toolUseID
	entry.Entity = result.FilePath
	entry.IsWrite = true
	entry.LinesAdded = added
	entry.LinesRemoved = removed
	entry.Files = []usage.FileChange{{Path: result.FilePath, LinesAdded: added, LinesRemoved: removed}}
	entry.Language = langdetect.FromPath(result.FilePath)
	return entry, true
}

// toolUseIDFromResult reads the tool_use_id off the tool_result block in the
// line's message content. Claude Code writes one result per line.
func toolUseIDFromResult(raw map[string]json.RawMessage) string {
	var message struct {
		Content []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"content"`
	}
	if !decodeField(raw, "message", &message) {
		return ""
	}
	for _, block := range message.Content {
		if block.Type == "tool_result" && block.ToolUseID != "" {
			return block.ToolUseID
		}
	}
	return ""
}

// ExtractSessionID derives the session id from a usage file's location under
// the projects directory: projects/<project>/<session>.jsonl for sessions and
// projects/<project>/<session>/subagents/<agent>.jsonl for subagents.
func ExtractSessionID(path string) string {
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
		return fileSessionID
	}
	if len(relative) >= 4 && relative[len(relative)-2] == "subagents" {
		return relative[len(relative)-3]
	}
	if len(relative) >= 2 {
		return relative[len(relative)-2]
	}
	return "unknown"
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Text  string          `json:"text"`
}

func languageFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return langdetect.Unknown
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return languageFromPathsInText(text)
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return langdetect.Unknown
	}

	candidates := make([]langdetect.Candidate, 0)
	for _, block := range blocks {
		if block.Type == "tool_use" {
			candidates = append(candidates, candidatesFromToolInput(block.Input)...)
		}
		if block.Text != "" {
			candidates = append(candidates, candidatesFromTextValue(block.Text, 1)...)
		}
	}

	return langdetect.Dominant(candidates)
}

func candidatesFromToolInput(raw json.RawMessage) []langdetect.Candidate {
	if len(raw) == 0 {
		return nil
	}

	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	return candidatesFromValue(input, 3)
}

func candidatesFromValue(value any, weight int) []langdetect.Candidate {
	candidates := make([]langdetect.Candidate, 0)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lowerKey := strings.ToLower(key)
			if isPathKey(lowerKey) {
				candidates = append(candidates, candidatesFromPathValue(child, weight)...)
				continue
			}
			if isTextKey(lowerKey) {
				candidates = append(candidates, candidatesFromTextValue(child, 1)...)
			}
		}
	case []any:
		for _, child := range typed {
			candidates = append(candidates, candidatesFromValue(child, weight)...)
		}
	}
	return candidates
}

func candidatesFromPathValue(value any, weight int) []langdetect.Candidate {
	switch typed := value.(type) {
	case string:
		if langdetect.FromPath(typed) != langdetect.Unknown {
			return []langdetect.Candidate{{Path: typed, Weight: weight}}
		}
		return candidatesFromTextValue(typed, 1)
	case []any:
		candidates := make([]langdetect.Candidate, 0, len(typed))
		for _, child := range typed {
			candidates = append(candidates, candidatesFromPathValue(child, weight)...)
		}
		return candidates
	default:
		return candidatesFromValue(value, weight)
	}
}

func candidatesFromTextValue(value any, weight int) []langdetect.Candidate {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	paths := langdetect.PathsFromText(text)
	candidates := make([]langdetect.Candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, langdetect.Candidate{Path: path, Weight: weight})
	}
	return candidates
}

func isPathKey(key string) bool {
	return strings.Contains(key, "file") || strings.Contains(key, "path")
}

func isTextKey(key string) bool {
	return strings.Contains(key, "command") || strings.Contains(key, "content") || strings.Contains(key, "query")
}

func languageFromPathsInText(text string) string {
	paths := langdetect.PathsFromText(text)
	return langdetect.DominantFromPaths(paths)
}

// stableEntryID keys an api_call on message.id + requestId. Both are assigned
// per API round trip and both survive Claude Code copying a session's history
// into a forked session file, so the copy yields the same ID and is stored
// once.
//
// This composition is frozen: the server dedupes on the ID alone and has no
// way to recognise an event under a new one, so changing it would re-upload
// every Claude event ever sent.
func stableEntryID(entry LoadedEntry) string {
	requestID := ""
	if entry.Data.RequestID != nil {
		requestID = *entry.Data.RequestID
	}
	return usage.StableID(
		string(usage.ProviderClaude),
		*entry.Data.Message.ID,
		requestID,
	)
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

// cacheCreation5m/1h read the TTL breakdown, verbatim. No arithmetic here:
// the collector ships facts, the server owns clamping and interpretation.
func cacheCreation5m(usage TokenUsage) uint64 {
	if usage.CacheCreation == nil {
		return 0
	}
	return usage.CacheCreation.Ephemeral5mInputTokens
}

func cacheCreation1h(usage TokenUsage) uint64 {
	if usage.CacheCreation == nil {
		return 0
	}
	return usage.CacheCreation.Ephemeral1hInputTokens
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

func pathParts(path string) []string {
	clean := filepath.Clean(path)
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return parts
}
