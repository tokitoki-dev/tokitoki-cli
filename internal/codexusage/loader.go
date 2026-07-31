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

	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
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
	Type    string `json:"type"`
	CallID  string `json:"call_id"`
	Success *bool  `json:"success"`
	Info    struct {
		LastTokenUsage  *tokenUsagePayload `json:"last_token_usage"`
		TotalTokenUsage *tokenUsagePayload `json:"total_token_usage"`
	} `json:"info"`
}

type tokenUsagePayload struct {
	InputTokens           uint64 `json:"input_tokens"`
	CachedInputTokens     uint64 `json:"cached_input_tokens"`
	OutputTokens          uint64 `json:"output_tokens"`
	ReasoningOutputTokens uint64 `json:"reasoning_output_tokens"`
	TotalTokens           uint64 `json:"total_tokens"`
}

func LoadEntriesFromPaths(paths []string, projectFilter string, fileFilter usage.FileFilter) ([]usage.Entry, error) {
	files := UsageFiles(paths)
	entries := make([]usage.Entry, 0)
	for _, file := range files {
		if fileFilter != nil && !fileFilter(file) {
			continue
		}
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
	language    string
	client      string
	// awaiting holds parsed patches keyed by call_id until their tool output
	// confirms the patch actually applied; pending holds confirmed patches
	// waiting to be folded into the next token_count entry.
	awaiting map[string][]patchFile
	pending  []patchFile
	// prevTotal is the last seen cumulative token counter, the basis for
	// per-event deltas.
	prevTotal *tokenUsagePayload
}

type patchFile struct {
	path    string
	added   uint64
	removed uint64
}

func parseLine(line []byte, state *fileState) (usage.Entry, bool) {
	if !bytes.Contains(line, []byte(`"type"`)) {
		return usage.Entry{}, false
	}

	var envelope codexLine
	if err := json.Unmarshal(line, &envelope); err != nil {
		return usage.Entry{}, false
	}

	if language := languageFromPayload(envelope.Payload); language != langdetect.Unknown {
		state.language = language
	}

	switch envelope.Type {
	case "response_item":
		handleResponseItem(envelope.Payload, state)
		return usage.Entry{}, false
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
		// Newer codex confirms patches with a dedicated event instead of a
		// tool output; either resolves the same awaiting call_id.
		if payload.Type == "patch_apply_end" {
			if patches, ok := state.awaiting[payload.CallID]; ok {
				delete(state.awaiting, payload.CallID)
				if payload.Success != nil && *payload.Success {
					state.pending = append(state.pending, patches...)
				}
			}
			return usage.Entry{}, false
		}
		if payload.Type != "token_count" || payload.Info.LastTokenUsage == nil {
			return usage.Entry{}, false
		}
		timestamp, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err != nil {
			return usage.Entry{}, false
		}
		last := *payload.Info.LastTokenUsage

		// The cumulative counter is authoritative: codex replays the same
		// last_token_usage across duplicate emissions and retries, so summing
		// it overcounts (up to +50% on real sessions). Each entry's usage is
		// the counter delta; last_token_usage covers files without a counter
		// and counter resets. The id stays derived from last_token_usage so
		// this accounting change never shifts event identity.
		event := last
		if total := payload.Info.TotalTokenUsage; total != nil {
			if state.prevTotal == nil {
				event = *total
			} else if delta, ok := diffTokenUsage(*total, *state.prevTotal); ok {
				if delta == (tokenUsagePayload{}) {
					state.prevTotal = total
					return usage.Entry{}, false
				}
				event = delta
			}
			state.prevTotal = total
		}

		entry := usage.Entry{
			Provider:    usage.ProviderCodex,
			Timestamp:   timestamp,
			Date:        timestamp.In(time.Local).Format("2006-01-02"),
			Project:     projectName(state.projectPath),
			ProjectPath: state.projectPath,
			SessionID:   state.sessionID,
			Model:       state.model,
			Language:    stateLanguage(state),
			OS:          usage.NormalizeOS(runtime.GOOS),
			Client:      state.client,
			Usage:       accountUsage(event),
		}
		idEntry := entry
		idEntry.Usage = accountUsage(last)
		entry.ID = StableEntryID(idEntry)
		applyPatches(&entry, state.pending)
		state.pending = nil
		return entry, true
	default:
		return usage.Entry{}, false
	}
}

func stateLanguage(state *fileState) string {
	return usage.NormalizeLanguage(state.language)
}

// accountUsage maps a raw codex token payload to our accounting: input_tokens
// is the FULL prompt (cached + non-cached), so real input = input - cached
// and the cached portion is cache read. Matches ccusage.
func accountUsage(tokens tokenUsagePayload) usage.TokenUsage {
	nonCached := uint64(0)
	if tokens.CachedInputTokens <= tokens.InputTokens {
		nonCached = tokens.InputTokens - tokens.CachedInputTokens
	}
	return usage.TokenUsage{
		InputTokens:           nonCached,
		CacheReadInputTokens:  tokens.CachedInputTokens,
		OutputTokens:          tokens.OutputTokens,
		ReasoningOutputTokens: tokens.ReasoningOutputTokens,
		TotalTokens:           tokens.TotalTokens,
	}
}

// diffTokenUsage reports the counter movement between two cumulative
// snapshots. ok is false when any field went backwards — a counter reset —
// and the caller falls back to last_token_usage.
func diffTokenUsage(current, previous tokenUsagePayload) (tokenUsagePayload, bool) {
	if previous.InputTokens > current.InputTokens ||
		previous.CachedInputTokens > current.CachedInputTokens ||
		previous.OutputTokens > current.OutputTokens ||
		previous.ReasoningOutputTokens > current.ReasoningOutputTokens ||
		previous.TotalTokens > current.TotalTokens {
		return tokenUsagePayload{}, false
	}
	return tokenUsagePayload{
		InputTokens:           current.InputTokens - previous.InputTokens,
		CachedInputTokens:     current.CachedInputTokens - previous.CachedInputTokens,
		OutputTokens:          current.OutputTokens - previous.OutputTokens,
		ReasoningOutputTokens: current.ReasoningOutputTokens - previous.ReasoningOutputTokens,
		TotalTokens:           current.TotalTokens - previous.TotalTokens,
	}, true
}

// handleResponseItem tracks file-modifying tool calls. A patch is parsed from
// the call, held until its output confirms success, then folded into the next
// token_count entry — the token event for the turn follows its tool calls.
func handleResponseItem(raw json.RawMessage, state *fileState) {
	var item struct {
		Type      string          `json:"type"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Input     string          `json:"input"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.CallID == "" {
		return
	}

	switch item.Type {
	case "custom_tool_call", "function_call":
		patches := patchesFromCall(item.Input, item.Arguments, state.projectPath)
		if len(patches) > 0 {
			if state.awaiting == nil {
				state.awaiting = make(map[string][]patchFile)
			}
			state.awaiting[item.CallID] = patches
		}
	case "custom_tool_call_output", "function_call_output":
		patches, ok := state.awaiting[item.CallID]
		if !ok {
			return
		}
		delete(state.awaiting, item.CallID)
		if outputSucceeded(item.Output) {
			state.pending = append(state.pending, patches...)
		}
	}
}

// patchesFromCall finds the apply_patch envelope in a tool call: directly in
// input, escaped inside a JSON-encoded input, or inside a shell heredoc in
// the JSON-encoded arguments.
func patchesFromCall(input, arguments, cwd string) []patchFile {
	if strings.Contains(input, patchBegin) {
		if patches := parsePatchEnvelope(input, cwd); len(patches) > 0 {
			return patches
		}
		var decoded any
		if err := json.Unmarshal([]byte(input), &decoded); err == nil {
			if text := findPatchString(decoded); text != "" {
				return parsePatchEnvelope(text, cwd)
			}
		}
		return nil
	}
	if !strings.Contains(arguments, patchBegin) {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return nil
	}
	if text := findPatchString(decoded); text != "" {
		return parsePatchEnvelope(text, cwd)
	}
	return nil
}

func findPatchString(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.Contains(typed, patchBegin) {
			return typed
		}
	case []any:
		for _, child := range typed {
			if text := findPatchString(child); text != "" {
				return text
			}
		}
	case map[string]any:
		for _, child := range typed {
			if text := findPatchString(child); text != "" {
				return text
			}
		}
	}
	return ""
}

const patchBegin = "*** Begin Patch"

// parsePatchEnvelope counts added and removed lines per file in codex's
// apply_patch format: "*** Add File: p", "*** Update File: p" and
// "*** Delete File: p" sections whose body lines carry +/- prefixes.
// Relative paths resolve against cwd; "*** Move to:" renames the section's
// file so the change lands on the destination path.
func parsePatchEnvelope(text, cwd string) []patchFile {
	start := strings.Index(text, patchBegin)
	if start < 0 {
		return nil
	}
	files := make([]patchFile, 0)
	var current *patchFile
	for _, line := range strings.Split(text[start:], "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "),
			strings.HasPrefix(line, "*** Update File: "),
			strings.HasPrefix(line, "*** Delete File: "):
			path := resolvePatchPath(cwd, line[strings.Index(line, ": ")+2:])
			if path == "" {
				current = nil
				continue
			}
			files = append(files, patchFile{path: path})
			current = &files[len(files)-1]
		case strings.HasPrefix(line, "*** Move to: "):
			if current != nil {
				if moved := resolvePatchPath(cwd, strings.TrimPrefix(line, "*** Move to: ")); moved != "" {
					current.path = moved
				}
			}
		case strings.HasPrefix(line, "*** End Patch"):
			current = nil
		case strings.HasPrefix(line, "***"):
			// Other directives keep the current file.
		case current == nil:
		case strings.HasPrefix(line, "+"):
			current.added++
		case strings.HasPrefix(line, "-"):
			current.removed++
		}
	}
	return files
}

func resolvePatchPath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || strings.TrimSpace(cwd) == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

// outputSucceeded reports whether a tool output confirms the patch applied.
// The output is either a plain string or an object with output/exit_code;
// apply_patch prints "Success." and shell wrappers prepend "Exit code: 0".
func outputSucceeded(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return patchOutputOK(text)
	}
	var items []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err == nil {
		var joined strings.Builder
		for _, item := range items {
			joined.WriteString(item.Text)
			joined.WriteByte('\n')
		}
		return patchOutputOK(joined.String())
	}
	var structured struct {
		Output   string `json:"output"`
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &structured); err != nil {
		return false
	}
	if structured.Metadata.ExitCode != nil {
		return *structured.Metadata.ExitCode == 0
	}
	return patchOutputOK(structured.Output)
}

func patchOutputOK(text string) bool {
	return strings.Contains(text, "Success.") || strings.HasPrefix(text, "Exit code: 0")
}

// applyPatches folds confirmed patches into the entry: totals, per-file
// breakdown, and the entity as the most-changed file.
func applyPatches(entry *usage.Entry, patches []patchFile) {
	if len(patches) == 0 {
		return
	}
	isWrite := true
	entry.IsWrite = &isWrite
	for _, patch := range patches {
		entry.LinesAdded += patch.added
		entry.LinesRemoved += patch.removed
		index := -1
		for i := range entry.Files {
			if entry.Files[i].Path == patch.path {
				index = i
				break
			}
		}
		if index < 0 {
			entry.Files = append(entry.Files, usage.FileChange{Path: patch.path})
			index = len(entry.Files) - 1
		}
		entry.Files[index].LinesAdded += patch.added
		entry.Files[index].LinesRemoved += patch.removed
	}
	best := 0
	for i := range entry.Files {
		if entry.Files[i].LinesAdded+entry.Files[i].LinesRemoved > entry.Files[best].LinesAdded+entry.Files[best].LinesRemoved {
			best = i
		}
	}
	entry.Entity = entry.Files[best].Path
	entry.EntityType = "file"
}

func languageFromPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return langdetect.Unknown
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return langdetect.Unknown
	}
	candidates := candidatesFromGenericValue(payload, 1)

	if arguments, ok := payload["arguments"].(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
			candidates = append(candidates, candidatesFromGenericValue(parsed, 3)...)
		} else {
			candidates = append(candidates, candidatesFromText(arguments, 1)...)
		}
	}

	if input, ok := payload["input"].(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(input), &parsed); err == nil {
			candidates = append(candidates, candidatesFromGenericValue(parsed, 3)...)
		} else {
			candidates = append(candidates, candidatesFromText(input, 1)...)
		}
	}

	return langdetect.Dominant(candidates)
}

func candidatesFromGenericValue(value any, weight int) []langdetect.Candidate {
	candidates := make([]langdetect.Candidate, 0)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if langdetect.FromPath(key) != langdetect.Unknown {
				candidates = append(candidates, langdetect.Candidate{Path: key, Weight: weight})
			}

			lowerKey := strings.ToLower(key)
			switch {
			case strings.Contains(lowerKey, "file") || strings.Contains(lowerKey, "path"):
				candidates = append(candidates, candidatesFromPathValue(child, weight+2)...)
			case strings.Contains(lowerKey, "command") || lowerKey == "cmd" || strings.Contains(lowerKey, "query") || strings.Contains(lowerKey, "content"):
				candidates = append(candidates, candidatesFromTextValue(child, weight)...)
			default:
				candidates = append(candidates, candidatesFromGenericValue(child, weight)...)
			}
		}
	case []any:
		for _, child := range typed {
			candidates = append(candidates, candidatesFromGenericValue(child, weight)...)
		}
	case string:
		if langdetect.FromPath(typed) != langdetect.Unknown {
			candidates = append(candidates, langdetect.Candidate{Path: typed, Weight: weight})
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
		return candidatesFromText(typed, 1)
	case []any:
		candidates := make([]langdetect.Candidate, 0, len(typed))
		for _, child := range typed {
			candidates = append(candidates, candidatesFromPathValue(child, weight)...)
		}
		return candidates
	default:
		return candidatesFromGenericValue(value, weight)
	}
}

func candidatesFromTextValue(value any, weight int) []langdetect.Candidate {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return candidatesFromText(text, weight)
}

func candidatesFromText(text string, weight int) []langdetect.Candidate {
	paths := langdetect.PathsFromText(text)
	candidates := make([]langdetect.Candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, langdetect.Candidate{Path: path, Weight: weight})
	}
	return candidates
}

// StableEntryID computes the deterministic id of a codex usage entry from the
// event itself — session, time, model and token counts — never from where the
// event is stored. Storage details (path, filename, line number) change when
// codex archives or rewrites a rollout file, and every id built on them
// eventually double-counts. Two token events in one session sharing the same
// millisecond and identical counts are the same event; collapsing them is
// correct.
func StableEntryID(entry usage.Entry) string {
	return usage.StableID(
		string(usage.ProviderCodex),
		entry.SessionID,
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
		return usage.UnknownProject
	}
	base := filepath.Base(filepath.Clean(clean))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return usage.UnknownProject
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
