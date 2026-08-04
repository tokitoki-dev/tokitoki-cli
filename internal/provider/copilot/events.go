package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Each Copilot CLI session keeps a transcript in
// ~/.copilot/session-state/<session>/events.jsonl. The session-store database
// is the source of truth for tokens; the transcript contributes what the
// database lacks: which files were modified and how many lines changed, plus
// the working directory for sessions missing from the sessions table.
//
// Everything the transcript contributes is timestamped rather than keyed by
// turn: the transcript's turnId counts agent-loop iterations while the
// database's turn_index counts user messages, so the two numberings never
// align and time is the only shared axis.

// Weights for the paths a session touched, mirroring the other providers:
// writing a file says the most about what is being worked on, reading less,
// and a path merely mentioned in a shell command or search pattern least.
const (
	writeWeight = 4
	readWeight  = 2
	textWeight  = 1
)

type timedChange struct {
	timestamp time.Time
	change    usage.FileChange
}

type timedCandidate struct {
	timestamp time.Time
	candidate langdetect.Candidate
}

type sessionContext struct {
	cwd     string
	gitRoot string
	branch  string

	changes    []timedChange
	candidates []timedCandidate
	// shutdownChanges holds the session summary's modified files, used only
	// for paths no tool telemetry already reported.
	shutdownChanges []usage.FileChange
}

func (c *sessionContext) projectDir() string {
	if c == nil {
		return ""
	}
	if c.gitRoot != "" {
		return c.gitRoot
	}
	return c.cwd
}

func (c *sessionContext) addCandidate(timestamp time.Time, candidate langdetect.Candidate) {
	c.candidates = append(c.candidates, timedCandidate{timestamp: timestamp, candidate: candidate})
}

// loadSessionContexts parses every session transcript under the given
// session-state directories. Transcripts the filter rejects are skipped: the
// usage entries they would have enriched are already ingested.
func loadSessionContexts(stateDirs []string, filter usage.FileFilter) map[string]*sessionContext {
	contexts := make(map[string]*sessionContext)
	for _, dir := range stateDirs {
		sessions, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			path := filepath.Join(dir, session.Name(), "events.jsonl")
			if filter != nil && !filter(path) {
				continue
			}
			context := parseSessionEvents(path)
			if context == nil {
				continue
			}
			contexts[session.Name()] = context
		}
	}
	return contexts
}

func parseSessionEvents(path string) *sessionContext {
	lines, err := agentdata.ReadJSONLines(path)
	if err != nil || len(lines) == 0 {
		return nil
	}

	context := &sessionContext{}
	// tool.execution_start carries the tool name and arguments;
	// tool.execution_complete carries the telemetry. The call id joins them.
	toolNames := make(map[string]string)

	for _, line := range lines {
		record := line.Value
		data := agentdata.ObjectAt(record["data"])
		if data == nil {
			continue
		}
		timestamp, _ := agentdata.ParseTimestamp(record["timestamp"])
		switch agentdata.StringField(record, "type") {
		case "session.start":
			handleSessionStart(data, context)
		case "tool.execution_start":
			handleToolStart(data, timestamp, context, toolNames)
		case "tool.execution_complete":
			handleToolComplete(data, timestamp, context, toolNames)
		case "session.shutdown":
			handleShutdown(data, context)
		}
	}
	return context
}

func handleSessionStart(data map[string]any, context *sessionContext) {
	block := agentdata.ObjectAt(data["context"])
	if block == nil {
		return
	}
	context.cwd = agentdata.FirstNonEmpty(agentdata.StringField(block, "cwd"), context.cwd)
	context.gitRoot = agentdata.FirstNonEmpty(agentdata.StringField(block, "gitRoot"), context.gitRoot)
	context.branch = agentdata.FirstNonEmpty(agentdata.StringField(block, "branch"), context.branch)
}

func handleToolStart(data map[string]any, timestamp time.Time, context *sessionContext, toolNames map[string]string) {
	callID := agentdata.StringField(data, "toolCallId")
	name := agentdata.FirstStringField(data, "toolName", "name")
	if callID != "" && name != "" {
		toolNames[callID] = name
	}
	for _, candidate := range argumentCandidates(agentdata.ObjectAt(data["arguments"]), context.cwd) {
		context.addCandidate(timestamp, candidate)
	}
}

// argumentCandidates extracts language evidence from a tool's arguments: the
// file a read names outright, or the paths a command or pattern mentions.
func argumentCandidates(arguments map[string]any, cwd string) []langdetect.Candidate {
	if arguments == nil {
		return nil
	}
	candidates := make([]langdetect.Candidate, 0)
	if path := agentdata.StringField(arguments, "path"); path != "" {
		candidates = append(candidates, langdetect.Candidate{Path: usage.ResolvePath(cwd, path), Weight: readWeight})
	}
	for _, value := range agentdata.ArrayAt(arguments["paths"]) {
		if path := agentdata.StringValue(value); path != "" {
			candidates = append(candidates, langdetect.Candidate{Path: usage.ResolvePath(cwd, path), Weight: readWeight})
		}
	}
	for _, key := range []string{"command", "pattern", "query"} {
		for _, path := range langdetect.PathsFromText(agentdata.StringField(arguments, key)) {
			candidates = append(candidates, langdetect.Candidate{Path: path, Weight: textWeight})
		}
	}
	return candidates
}

func handleToolComplete(data map[string]any, timestamp time.Time, context *sessionContext, toolNames map[string]string) {
	if success, ok := data["success"].(bool); ok && !success {
		return
	}
	callID := agentdata.StringField(data, "toolCallId")
	tool := agentdata.FirstStringField(data, "toolName", "name")
	if tool == "" {
		tool = toolNames[callID]
	}

	telemetry := toolTelemetry(data)
	if telemetry == nil || !hasWriteSignals(tool, telemetry) {
		return
	}
	paths := telemetryFilePaths(telemetry)
	if len(paths) == 0 {
		return
	}
	blocks := telemetryCodeBlocks(telemetry, len(paths))
	for i, path := range paths {
		if !trackablePath(path) {
			continue
		}
		change := usage.FileChange{Path: usage.ResolvePath(context.cwd, path)}
		if blocks != nil {
			change.LinesAdded = blocks[i].added
			change.LinesRemoved = blocks[i].removed
		}
		context.changes = append(context.changes, timedChange{timestamp: timestamp, change: change})
		context.addCandidate(timestamp, langdetect.Candidate{Path: change.Path, Weight: writeWeight})
	}
}

// handleShutdown records the session summary's code changes so files that no
// tool telemetry reported still surface, on CLI versions without it.
func handleShutdown(data map[string]any, context *sessionContext) {
	changes := agentdata.ObjectAt(data["codeChanges"])
	if changes == nil {
		return
	}
	files := stringValues(changes["filesModified"])
	added := agentdata.UintField(changes, "linesAdded")
	removed := agentdata.UintField(changes, "linesRemoved")
	for _, path := range files {
		if !trackablePath(path) {
			continue
		}
		change := usage.FileChange{Path: usage.ResolvePath(context.cwd, path)}
		// The summary totals are per session, not per file; they are only
		// attributable when a single file changed.
		if len(files) == 1 {
			change.LinesAdded = added
			change.LinesRemoved = removed
		}
		context.shutdownChanges = append(context.shutdownChanges, change)
	}
}

// toolTelemetry finds the telemetry block, which moved between CLI versions:
// toolTelemetry, then toolResultTelemetry, then result.toolTelemetry.
func toolTelemetry(data map[string]any) map[string]any {
	if telemetry := agentdata.ObjectAt(data["toolTelemetry"]); telemetry != nil {
		return telemetry
	}
	if telemetry := agentdata.ObjectAt(data["toolResultTelemetry"]); telemetry != nil {
		return telemetry
	}
	return agentdata.ObjectAt(agentdata.ObjectAt(data["result"])["toolTelemetry"])
}

func hasWriteSignals(tool string, telemetry map[string]any) bool {
	switch tool {
	case "write", "edit", "apply_patch", "str_replace_editor", "create":
		return true
	}
	metrics := agentdata.ObjectAt(telemetry["metrics"])
	if metrics != nil && (metrics["linesAdded"] != nil || metrics["linesRemoved"] != nil) {
		return true
	}
	restricted := agentdata.ObjectAt(telemetry["restrictedProperties"])
	return len(stringValues(restricted["addedPaths"])) > 0 ||
		len(stringValues(restricted["deletedPaths"])) > 0
}

func telemetryFilePaths(telemetry map[string]any) []string {
	restricted := agentdata.ObjectAt(telemetry["restrictedProperties"])
	if restricted == nil {
		return nil
	}
	paths := stringValues(restricted["filePaths"])
	if len(paths) == 0 {
		paths = append(paths, stringValues(restricted["addedPaths"])...)
		paths = append(paths, stringValues(restricted["deletedPaths"])...)
	}
	return agentdata.UniqueStrings(paths)
}

type lineCounts struct {
	added   uint64
	removed uint64
}

// telemetryCodeBlocks aligns per-file line counts with the file list. The
// codeBlocks array matches when the tool reported one block per file; the
// flat metrics only apply when a single file changed.
func telemetryCodeBlocks(telemetry map[string]any, pathCount int) []lineCounts {
	blocks := arrayValues(agentdata.ObjectAt(telemetry["properties"])["codeBlocks"])
	if len(blocks) == pathCount {
		counts := make([]lineCounts, 0, len(blocks))
		for _, block := range blocks {
			counts = append(counts, lineCounts{
				added:   agentdata.UintField(agentdata.ObjectAt(block), "linesAdded"),
				removed: agentdata.UintField(agentdata.ObjectAt(block), "linesRemoved"),
			})
		}
		return counts
	}
	metrics := agentdata.ObjectAt(telemetry["metrics"])
	if pathCount == 1 && metrics != nil && (metrics["linesAdded"] != nil || metrics["linesRemoved"] != nil) {
		return []lineCounts{{
			added:   agentdata.UintField(metrics, "linesAdded"),
			removed: agentdata.UintField(metrics, "linesRemoved"),
		}}
	}
	return nil
}

// trackablePath rejects the CLI's own bookkeeping files, like the plan.md it
// writes under its session-state directory.
func trackablePath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return !strings.Contains(filepath.ToSlash(path), ".copilot/session-state/")
}

// stringValues decodes a value that is either a JSON array of strings or a
// string holding an encoded JSON array, which is how telemetry properties
// arrive.
func stringValues(value any) []string {
	values := make([]string, 0)
	for _, entry := range arrayValues(value) {
		if text := agentdata.StringValue(entry); text != "" {
			values = append(values, text)
		}
	}
	return values
}

func arrayValues(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case string:
		var decoded []any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return nil
		}
		return decoded
	default:
		return nil
	}
}
