package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// dataRoots resolves what to scan from the configured paths. A Copilot data
// root holds up to three sources: otel/ with the OpenTelemetry export older
// CLI versions wrote, session-store.db with the usage database current
// versions write, and session-state/ with the per-session transcripts.
type dataRoots struct {
	otelDirs  []string
	storeDBs  []string
	stateDirs []string
}

func resolveDataRoots(paths []string) dataRoots {
	roots := dataRoots{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			// A file path selects one source directly: a database or one
			// OpenTelemetry export.
			if strings.EqualFold(filepath.Ext(path), ".db") {
				roots.storeDBs = append(roots.storeDBs, path)
			} else {
				roots.otelDirs = append(roots.otelDirs, path)
			}
			continue
		}
		// Any configured directory is scanned for OpenTelemetry exports, as
		// it always was. Deployed configurations point at ~/.copilot/otel;
		// the store and transcripts live next to it.
		roots.otelDirs = append(roots.otelDirs, path)
		root := path
		if filepath.Base(path) == "otel" {
			root = filepath.Dir(path)
		}
		roots.storeDBs = append(roots.storeDBs, filepath.Join(root, "session-store.db"))
		roots.stateDirs = append(roots.stateDirs, filepath.Join(root, "session-state"))
	}
	roots.otelDirs = agentdata.UniqueStrings(roots.otelDirs)
	roots.storeDBs = agentdata.UniqueStrings(roots.storeDBs)
	roots.stateDirs = agentdata.UniqueStrings(roots.stateDirs)
	return roots
}

func isSessionStateFile(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/session-state/")
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	roots := resolveDataRoots(paths)

	storeEvents := make([]storeEvent, 0)
	storeSessions := make(map[string]storeSession)
	for _, dbPath := range roots.storeDBs {
		if !agentdb.ExistingSQLiteFile(dbPath) {
			continue
		}
		if filter != nil && !filter(dbPath) {
			continue
		}
		events, sessions, err := loadStoreDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		storeEvents = append(storeEvents, events...)
		for id, session := range sessions {
			storeSessions[id] = session
		}
	}

	contexts := loadSessionContexts(roots.stateDirs, filter)
	entries := buildStoreEntries(storeEvents, storeSessions, contexts)

	// The OpenTelemetry export from older CLI versions. Entries the store
	// already accounts for are dropped: a CLI that writes both sources
	// records the same API call twice.
	seen := storeFingerprints(storeEvents)
	files := make([]string, 0)
	for _, root := range roots.otelDirs {
		for _, file := range agentdata.CollectExt(root, ".jsonl") {
			// The session transcripts are .jsonl too, but they are not
			// OpenTelemetry exports.
			if isSessionStateFile(file) {
				continue
			}
			files = append(files, file)
		}
	}
	sort.Strings(files)
	files = agentdata.FilterFiles(agentdata.UniqueStrings(files), filter)
	for _, file := range files {
		fileEntries, err := parseOTELFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range fileEntries {
			if seen.matches(entry.SessionID, entry.Model, entry.Usage, entry.Timestamp) {
				continue
			}
			entries = append(entries, entry)
		}
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

// buildStoreEntries turns the usage database rows into entries. The sessions
// table names the directory and branch each session ran in; the transcript
// contributes the file changes and language evidence, each attached to the
// session's last API call at or before its own timestamp — a diff follows
// the request that made it.
func buildStoreEntries(events []storeEvent, sessions map[string]storeSession, contexts map[string]*sessionContext) []usage.Entry {
	entries := make([]usage.Entry, 0, len(events))
	bySession := make(map[string][]int)
	for i, event := range events {
		session := sessions[event.sessionID]
		context := contexts[event.sessionID]

		cwd := agentdata.FirstNonEmpty(session.cwd, context.projectDir())
		project, projectPath := usage.UnknownProject, ""
		if path, name, ok := usage.ProjectFromCWD(cwd); ok {
			project, projectPath = name, path
		}

		entry := usageprovider.BaseEntry(
			usage.ProviderCopilot,
			event.timestamp,
			project,
			projectPath,
			event.sessionID,
			event.model,
			"GitHub Copilot CLI",
			event.tokens,
		)
		entry.Branch = agentdata.FirstNonEmpty(session.branch, branchOf(context))
		entry.ID = storeEntryID(event)
		entries = append(entries, entry)
		bySession[event.sessionID] = append(bySession[event.sessionID], i)
	}

	for sessionID, indexes := range bySession {
		context := contexts[sessionID]
		if context == nil {
			continue
		}
		attachSessionContext(entries, indexes, context)
	}
	return entries
}

// attachSessionContext distributes one session's transcript evidence over its
// entries. indexes point into entries in database row order, which is time
// order.
func attachSessionContext(entries []usage.Entry, indexes []int, context *sessionContext) {
	// entryAt finds the entry an event at the given time belongs to: the last
	// API call at or before it, or the first call for evidence that precedes
	// the whole session's usage.
	entryAt := func(timestamp time.Time) int {
		target := indexes[0]
		for _, index := range indexes {
			if entries[index].Timestamp.After(timestamp) {
				break
			}
			target = index
		}
		return target
	}

	seen := make(map[string]bool)
	for _, change := range context.changes {
		entries[entryAt(change.timestamp)].ApplyFileChange(change.change)
		seen[change.change.Path] = true
	}
	// The shutdown summary covers files no telemetry reported; they land on
	// the session's last entry, the closest one to shutdown.
	last := &entries[indexes[len(indexes)-1]]
	for _, change := range context.shutdownChanges {
		if seen[change.Path] {
			continue
		}
		last.ApplyFileChange(change)
	}

	// Language: each entry judges the evidence recorded up to its own API
	// call; an entry without evidence of its own inherits the session-wide
	// verdict.
	sessionWide := make([]langdetect.Candidate, 0, len(context.candidates))
	perEntry := make(map[int][]langdetect.Candidate)
	for _, candidate := range context.candidates {
		sessionWide = append(sessionWide, candidate.candidate)
		index := entryAt(candidate.timestamp)
		perEntry[index] = append(perEntry[index], candidate.candidate)
	}
	fallback := langdetect.Dominant(sessionWide)
	for _, index := range indexes {
		language := langdetect.Dominant(perEntry[index])
		if language == langdetect.Unknown {
			language = fallback
		}
		entries[index].Language = usage.NormalizeLanguage(language)
	}
}

func branchOf(context *sessionContext) string {
	if context == nil {
		return ""
	}
	return context.branch
}

// storeEntryID identifies a database row by its content, not its position:
// the store has no cross-machine row identity, and content plus timestamp is
// stable across re-scans.
func storeEntryID(event storeEvent) string {
	return usage.StableID(
		string(usage.ProviderCopilot),
		"store-event",
		event.sessionID,
		event.turnKey,
		event.model,
		event.timestamp.UTC().Format(time.RFC3339Nano),
		strconv.FormatUint(event.tokens.InputTokens, 10),
		strconv.FormatUint(event.tokens.OutputTokens, 10),
		strconv.FormatUint(event.tokens.CacheCreationInputTokens, 10),
		strconv.FormatUint(event.tokens.CacheReadInputTokens, 10),
		strconv.FormatUint(event.tokens.ReasoningOutputTokens, 10),
	)
}

// fingerprintSet answers whether the store already recorded an API call an
// OpenTelemetry record describes: same session, model and token counts,
// within a two-second window of each other.
type fingerprintSet map[string][]time.Time

func storeFingerprints(events []storeEvent) fingerprintSet {
	set := make(fingerprintSet, len(events))
	for _, event := range events {
		key := fingerprintKey(event.sessionID, event.model, event.tokens)
		set[key] = append(set[key], event.timestamp)
	}
	return set
}

func (s fingerprintSet) matches(sessionID, model string, tokens usage.TokenUsage, timestamp time.Time) bool {
	const tolerance = 2 * time.Second
	for _, candidate := range s[fingerprintKey(sessionID, model, tokens)] {
		delta := candidate.Sub(timestamp)
		if delta < 0 {
			delta = -delta
		}
		if delta <= tolerance {
			return true
		}
	}
	return false
}

func fingerprintKey(sessionID, model string, tokens usage.TokenUsage) string {
	return strings.Join([]string{
		sessionID,
		model,
		strconv.FormatUint(tokens.InputTokens, 10),
		strconv.FormatUint(tokens.OutputTokens, 10),
		strconv.FormatUint(tokens.CacheCreationInputTokens, 10),
		strconv.FormatUint(tokens.CacheReadInputTokens, 10),
		strconv.FormatUint(tokens.ReasoningOutputTokens, 10),
	}, "|")
}

type sourceKind int

const (
	copilotChatSpan sourceKind = iota
	copilotInferenceLog
	copilotAgentTurnLog
	copilotAgentSummarySpan
)

type candidate struct {
	source                 sourceKind
	traceID                string
	responseID             string
	sessionID              string
	model                  string
	timestamp              time.Time
	tokens                 usage.TokenUsage
	dedupKey               string
	sourceFile             string
	sourceLine             int
	sourceStart, sourceEnd int64
}

type traceContext struct {
	model             string
	sessionID         string
	sessionIDPriority int
}

func parseOTELFile(path string) ([]usage.Entry, error) {
	lines, err := agentdata.ReadJSONLines(path, `"attributes"`)
	if err != nil {
		return nil, err
	}
	contexts := traceContexts(lines)
	fallback := agentdata.FileModifiedTime(path)
	candidates := make([]candidate, 0)
	for index, line := range lines {
		if candidate, ok := recordCandidate(path, line, index, fallback, contexts); ok {
			candidates = append(candidates, candidate)
		}
	}
	sets := candidateSets(candidates)
	entries := make([]usage.Entry, 0)
	for _, candidate := range candidates {
		if !shouldEmitCopilot(candidate, sets) {
			continue
		}
		entry := usageprovider.BaseEntry(
			usage.ProviderCopilot,
			candidate.timestamp,
			"copilot",
			"GitHub Copilot CLI",
			candidate.sessionID,
			candidate.model,
			"GitHub Copilot CLI",
			candidate.tokens,
		)
		usageprovider.SetSource(&entry, candidate.sourceFile, candidate.sourceLine, candidate.sourceStart, candidate.sourceEnd)
		entry.ID = usageprovider.StableEntryID(entry, candidate.dedupKey)
		entries = append(entries, entry)
	}
	return entries, nil
}

func traceContexts(lines []agentdata.LineJSON) map[string]traceContext {
	contexts := make(map[string]traceContext)
	for _, line := range lines {
		record := line.Value
		traceID := traceID(record)
		if traceID == "" {
			continue
		}
		attrs := agentdata.ObjectAt(record["attributes"])
		if attrs == nil {
			continue
		}
		context := contexts[traceID]
		if context.model == "" {
			context.model = agentdata.FirstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
		}
		if sessionID, priority := bestSession(attrs); sessionID != "" && priority > context.sessionIDPriority {
			context.sessionID = sessionID
			context.sessionIDPriority = priority
		}
		contexts[traceID] = context
	}
	return contexts
}

func recordCandidate(path string, line agentdata.LineJSON, index int, fallback time.Time, contexts map[string]traceContext) (candidate, bool) {
	record := line.Value
	attrs := agentdata.ObjectAt(record["attributes"])
	if attrs == nil {
		return candidate{}, false
	}
	source, ok := recordSource(record, attrs)
	if !ok {
		return candidate{}, false
	}
	input := agentdata.UintField(attrs, "gen_ai.usage.input_tokens")
	cacheRead := agentdata.UintField(attrs, "gen_ai.usage.cache_read.input_tokens")
	if cacheRead <= input {
		input -= cacheRead
	} else {
		input = 0
	}
	tokens := usage.TokenUsage{
		InputTokens:              input,
		OutputTokens:             agentdata.UintField(attrs, "gen_ai.usage.output_tokens"),
		CacheCreationInputTokens: agentdata.UintField(attrs, "gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens"),
		CacheReadInputTokens:     cacheRead,
		ReasoningOutputTokens:    agentdata.UintField(attrs, "gen_ai.usage.reasoning.output_tokens", "gen_ai.usage.reasoning_tokens"),
	}
	tokens = usageprovider.ApplyTotalFallback(tokens, agentdata.UintField(attrs, "gen_ai.usage.total_tokens", "gen_ai.usage.total.token_count"))
	if !usageprovider.NonZero(tokens) {
		return candidate{}, false
	}
	traceID := traceID(record)
	context := contexts[traceID]
	model := agentdata.FirstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
	if model == "" {
		model = context.model
	}
	if model == "" {
		model = "unknown"
	}
	sessionID, _ := bestSession(attrs)
	if sessionID == "" {
		sessionID = context.sessionID
	}
	if sessionID == "" {
		sessionID = traceID
	}
	if sessionID == "" {
		sessionID = "unknown-session"
	}
	timestamp, ok := timestamp(record)
	if !ok {
		timestamp = fallback
	}
	responseID := agentdata.StringField(attrs, "gen_ai.response.id")
	return candidate{
		source:      source,
		traceID:     traceID,
		responseID:  responseID,
		sessionID:   sessionID,
		model:       model,
		timestamp:   timestamp,
		tokens:      tokens,
		dedupKey:    dedupKey(source, record, attrs, traceID, sessionID, timestamp, index),
		sourceFile:  path,
		sourceLine:  line.Line,
		sourceStart: line.Start,
		sourceEnd:   line.End,
	}, true
}

func recordSource(record, attrs map[string]any) (sourceKind, bool) {
	switch {
	case isChatSpan(record, attrs):
		return copilotChatSpan, true
	case isInferenceLog(record, attrs):
		return copilotInferenceLog, true
	case isAgentTurnLog(record, attrs):
		return copilotAgentTurnLog, true
	case isAgentSummarySpan(record, attrs):
		return copilotAgentSummarySpan, true
	default:
		return 0, false
	}
}

func isSpan(record map[string]any) bool {
	if agentdata.StringField(record, "type") == "span" {
		return true
	}
	if agentdata.StringField(record, "name") == "" {
		return false
	}
	return agentdata.StringField(record, "spanId") != "" ||
		agentdata.StringField(record, "traceId") != "" ||
		record["startTime"] != nil ||
		record["endTime"] != nil ||
		record["duration"] != nil ||
		record["kind"] != nil
}

func isChatSpan(record, attrs map[string]any) bool {
	return isSpan(record) &&
		(agentdata.StringField(attrs, "gen_ai.operation.name") == "chat" ||
			strings.HasPrefix(agentdata.StringField(record, "name"), "chat "))
}

func isAgentSummarySpan(record, attrs map[string]any) bool {
	return isSpan(record) &&
		(agentdata.StringField(attrs, "gen_ai.operation.name") == "invoke_agent" ||
			strings.HasPrefix(agentdata.StringField(record, "name"), "invoke_agent "))
}

func isInferenceLog(record, attrs map[string]any) bool {
	return !isSpan(record) &&
		(agentdata.StringField(attrs, "event.name") == "gen_ai.client.inference.operation.details" ||
			strings.HasPrefix(spanBody(record), "GenAI inference:"))
}

func isAgentTurnLog(record, attrs map[string]any) bool {
	return !isSpan(record) &&
		(agentdata.StringField(attrs, "event.name") == "copilot_chat.agent.turn" ||
			strings.HasPrefix(spanBody(record), "copilot_chat.agent.turn"))
}

func spanBody(record map[string]any) string {
	if body := agentdata.StringField(record, "body"); body != "" {
		return body
	}
	return agentdata.StringField(record, "_body")
}

func traceID(record map[string]any) string {
	if traceID := agentdata.StringField(record, "traceId"); traceID != "" {
		return traceID
	}
	return agentdata.StringField(agentdata.ObjectAt(record["spanContext"]), "traceId")
}

func spanID(record map[string]any) string {
	if spanID := agentdata.StringField(record, "spanId"); spanID != "" {
		return spanID
	}
	return agentdata.StringField(agentdata.ObjectAt(record["spanContext"]), "spanId")
}

func bestSession(attrs map[string]any) (string, int) {
	candidates := []struct {
		key      string
		priority int
	}{
		{"gen_ai.conversation.id", 3},
		{"copilot_chat.session_id", 3},
		{"copilot_chat.chat_session_id", 3},
		{"session.id", 3},
		{"github.copilot.interaction_id", 2},
		{"gen_ai.response.id", 1},
	}
	bestValue := ""
	bestPriority := 0
	for _, candidate := range candidates {
		if value := agentdata.StringField(attrs, candidate.key); value != "" && candidate.priority > bestPriority {
			bestValue = value
			bestPriority = candidate.priority
		}
	}
	return bestValue, bestPriority
}

func timestamp(record map[string]any) (time.Time, bool) {
	for _, key := range []string{"endTime", "startTime", "hrTime", "_hrTime", "time"} {
		if timestamp, ok := agentdata.TimestampFromParts(record[key]); ok {
			return timestamp, true
		}
	}
	for _, key := range []string{"timestamp", "observedTimestamp"} {
		if timestamp, ok := agentdata.ParseTimestamp(record[key]); ok {
			return timestamp, true
		}
	}
	if raw := agentdata.UintValue(record["timeUnixNano"]); raw > 0 {
		return time.UnixMilli(int64(raw / 1_000_000)), true
	}
	return time.Time{}, false
}

func dedupKey(source sourceKind, record, attrs map[string]any, traceID, sessionID string, timestamp time.Time, index int) string {
	spanID := spanID(record)
	switch source {
	case copilotChatSpan, copilotAgentSummarySpan:
		if traceID != "" && spanID != "" {
			return traceID + ":" + spanID
		}
		return fmt.Sprintf("span:%s:%d:%d", sessionID, timestamp.UnixMilli(), index)
	case copilotInferenceLog:
		if traceID != "" && spanID != "" {
			return "log:" + traceID + ":" + spanID
		}
		return fmt.Sprintf("log:%s:%d:%d", sessionID, timestamp.UnixMilli(), index)
	case copilotAgentTurnLog:
		turnIndex := agentdata.UintField(attrs, "turn.index", "copilot_chat.turn.index")
		turn := fmt.Sprintf("idx-%d", index)
		if turnIndex > 0 {
			turn = fmt.Sprintf("%d", turnIndex)
		}
		if traceID != "" {
			return "agent-turn:" + traceID + ":" + turn
		}
		return "agent-turn:" + sessionID + ":" + turn + fmt.Sprintf(":%d", index)
	default:
		return fmt.Sprintf("%s:%d", filepath.Base(sourceName(source)), index)
	}
}

func sourceName(source sourceKind) string {
	switch source {
	case copilotChatSpan:
		return "chat"
	case copilotInferenceLog:
		return "inference"
	case copilotAgentTurnLog:
		return "agent-turn"
	case copilotAgentSummarySpan:
		return "agent-summary"
	default:
		return "unknown"
	}
}

type candidateSetMap struct {
	chatTraces         map[string]bool
	inferenceTraces    map[string]bool
	agentTurnTraces    map[string]bool
	chatResponses      map[string]bool
	inferenceResponses map[string]bool
	agentTurnResponses map[string]bool
}

func candidateSets(candidates []candidate) candidateSetMap {
	sets := candidateSetMap{
		chatTraces:         make(map[string]bool),
		inferenceTraces:    make(map[string]bool),
		agentTurnTraces:    make(map[string]bool),
		chatResponses:      make(map[string]bool),
		inferenceResponses: make(map[string]bool),
		agentTurnResponses: make(map[string]bool),
	}
	for _, candidate := range candidates {
		if candidate.traceID != "" {
			switch candidate.source {
			case copilotChatSpan:
				sets.chatTraces[candidate.traceID] = true
			case copilotInferenceLog:
				sets.inferenceTraces[candidate.traceID] = true
			case copilotAgentTurnLog:
				sets.agentTurnTraces[candidate.traceID] = true
			}
		}
		if candidate.responseID != "" {
			switch candidate.source {
			case copilotChatSpan:
				sets.chatResponses[candidate.responseID] = true
			case copilotInferenceLog:
				sets.inferenceResponses[candidate.responseID] = true
			case copilotAgentTurnLog:
				sets.agentTurnResponses[candidate.responseID] = true
			}
		}
	}
	return sets
}

func shouldEmitCopilot(candidate candidate, sets candidateSetMap) bool {
	traceMatch := func(values map[string]bool) bool {
		return candidate.traceID != "" && values[candidate.traceID]
	}
	responseMatch := func(values map[string]bool) bool {
		return candidate.responseID != "" && values[candidate.responseID]
	}
	switch candidate.source {
	case copilotChatSpan:
		return true
	case copilotInferenceLog:
		return !traceMatch(sets.chatTraces) && !responseMatch(sets.chatResponses)
	case copilotAgentTurnLog:
		return !traceMatch(sets.chatTraces) &&
			!traceMatch(sets.inferenceTraces) &&
			!responseMatch(sets.chatResponses) &&
			!responseMatch(sets.inferenceResponses)
	case copilotAgentSummarySpan:
		return !traceMatch(sets.chatTraces) &&
			!traceMatch(sets.inferenceTraces) &&
			!traceMatch(sets.agentTurnTraces) &&
			!responseMatch(sets.chatResponses) &&
			!responseMatch(sets.inferenceResponses) &&
			!responseMatch(sets.agentTurnResponses)
	default:
		return false
	}
}
