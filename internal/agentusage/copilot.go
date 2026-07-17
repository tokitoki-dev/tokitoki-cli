package agentusage

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadCopilotEntries(paths []string) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, collectExt(root, ".jsonl")...)
	}
	sort.Strings(files)
	files = uniqueStrings(files)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseCopilotOTELFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	sortEntries(entries)
	return entries, nil
}

type copilotSource int

const (
	copilotChatSpan copilotSource = iota
	copilotInferenceLog
	copilotAgentTurnLog
	copilotAgentSummarySpan
)

type copilotCandidate struct {
	source                 copilotSource
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

type copilotTraceContext struct {
	model             string
	sessionID         string
	sessionIDPriority int
}

func parseCopilotOTELFile(path string) ([]usage.Entry, error) {
	lines, err := readJSONLines(path, `"attributes"`)
	if err != nil {
		return nil, err
	}
	contexts := copilotTraceContexts(lines)
	fallback := fileModifiedTime(path)
	candidates := make([]copilotCandidate, 0)
	for index, line := range lines {
		if candidate, ok := copilotRecordCandidate(path, line, index, fallback, contexts); ok {
			candidates = append(candidates, candidate)
		}
	}
	sets := copilotCandidateSets(candidates)
	entries := make([]usage.Entry, 0)
	for _, candidate := range candidates {
		if !shouldEmitCopilot(candidate, sets) {
			continue
		}
		entry := baseEntry(
			usage.ProviderCopilot,
			candidate.timestamp,
			"copilot",
			"GitHub Copilot CLI",
			candidate.sessionID,
			candidate.model,
			"GitHub Copilot CLI",
			candidate.tokens,
		)
		setSource(&entry, candidate.sourceFile, candidate.sourceLine, candidate.sourceStart, candidate.sourceEnd)
		entry.ID = stableEntryID(entry, candidate.dedupKey)
		entries = append(entries, entry)
	}
	return entries, nil
}

func copilotTraceContexts(lines []lineJSON) map[string]copilotTraceContext {
	contexts := make(map[string]copilotTraceContext)
	for _, line := range lines {
		record := line.value
		traceID := copilotTraceID(record)
		if traceID == "" {
			continue
		}
		attrs := objectAt(record["attributes"])
		if attrs == nil {
			continue
		}
		context := contexts[traceID]
		if context.model == "" {
			context.model = firstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
		}
		if sessionID, priority := copilotBestSession(attrs); sessionID != "" && priority > context.sessionIDPriority {
			context.sessionID = sessionID
			context.sessionIDPriority = priority
		}
		contexts[traceID] = context
	}
	return contexts
}

func copilotRecordCandidate(path string, line lineJSON, index int, fallback time.Time, contexts map[string]copilotTraceContext) (copilotCandidate, bool) {
	record := line.value
	attrs := objectAt(record["attributes"])
	if attrs == nil {
		return copilotCandidate{}, false
	}
	source, ok := copilotRecordSource(record, attrs)
	if !ok {
		return copilotCandidate{}, false
	}
	input := uintField(attrs, "gen_ai.usage.input_tokens")
	cacheRead := uintField(attrs, "gen_ai.usage.cache_read.input_tokens")
	if cacheRead <= input {
		input -= cacheRead
	} else {
		input = 0
	}
	tokens := usage.TokenUsage{
		InputTokens:              input,
		OutputTokens:             uintField(attrs, "gen_ai.usage.output_tokens"),
		CacheCreationInputTokens: uintField(attrs, "gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens"),
		CacheReadInputTokens:     cacheRead,
		ReasoningOutputTokens:    uintField(attrs, "gen_ai.usage.reasoning.output_tokens", "gen_ai.usage.reasoning_tokens"),
	}
	tokens = applyTotalFallback(tokens, uintField(attrs, "gen_ai.usage.total_tokens", "gen_ai.usage.total.token_count"))
	if !nonZero(tokens) {
		return copilotCandidate{}, false
	}
	traceID := copilotTraceID(record)
	context := contexts[traceID]
	model := firstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
	if model == "" {
		model = context.model
	}
	if model == "" {
		model = "unknown"
	}
	sessionID, _ := copilotBestSession(attrs)
	if sessionID == "" {
		sessionID = context.sessionID
	}
	if sessionID == "" {
		sessionID = traceID
	}
	if sessionID == "" {
		sessionID = "unknown-session"
	}
	timestamp, ok := copilotTimestamp(record)
	if !ok {
		timestamp = fallback
	}
	responseID := stringField(attrs, "gen_ai.response.id")
	return copilotCandidate{
		source:      source,
		traceID:     traceID,
		responseID:  responseID,
		sessionID:   sessionID,
		model:       model,
		timestamp:   timestamp,
		tokens:      tokens,
		dedupKey:    copilotDedupKey(source, record, attrs, traceID, sessionID, timestamp, index),
		sourceFile:  path,
		sourceLine:  line.line,
		sourceStart: line.start,
		sourceEnd:   line.end,
	}, true
}

func copilotRecordSource(record, attrs map[string]any) (copilotSource, bool) {
	switch {
	case copilotIsChatSpan(record, attrs):
		return copilotChatSpan, true
	case copilotIsInferenceLog(record, attrs):
		return copilotInferenceLog, true
	case copilotIsAgentTurnLog(record, attrs):
		return copilotAgentTurnLog, true
	case copilotIsAgentSummarySpan(record, attrs):
		return copilotAgentSummarySpan, true
	default:
		return 0, false
	}
}

func copilotIsSpan(record map[string]any) bool {
	if stringField(record, "type") == "span" {
		return true
	}
	if stringField(record, "name") == "" {
		return false
	}
	return stringField(record, "spanId") != "" ||
		stringField(record, "traceId") != "" ||
		record["startTime"] != nil ||
		record["endTime"] != nil ||
		record["duration"] != nil ||
		record["kind"] != nil
}

func copilotIsChatSpan(record, attrs map[string]any) bool {
	return copilotIsSpan(record) &&
		(stringField(attrs, "gen_ai.operation.name") == "chat" ||
			strings.HasPrefix(stringField(record, "name"), "chat "))
}

func copilotIsAgentSummarySpan(record, attrs map[string]any) bool {
	return copilotIsSpan(record) &&
		(stringField(attrs, "gen_ai.operation.name") == "invoke_agent" ||
			strings.HasPrefix(stringField(record, "name"), "invoke_agent "))
}

func copilotIsInferenceLog(record, attrs map[string]any) bool {
	return !copilotIsSpan(record) &&
		(stringField(attrs, "event.name") == "gen_ai.client.inference.operation.details" ||
			strings.HasPrefix(copilotBody(record), "GenAI inference:"))
}

func copilotIsAgentTurnLog(record, attrs map[string]any) bool {
	return !copilotIsSpan(record) &&
		(stringField(attrs, "event.name") == "copilot_chat.agent.turn" ||
			strings.HasPrefix(copilotBody(record), "copilot_chat.agent.turn"))
}

func copilotBody(record map[string]any) string {
	if body := stringField(record, "body"); body != "" {
		return body
	}
	return stringField(record, "_body")
}

func copilotTraceID(record map[string]any) string {
	if traceID := stringField(record, "traceId"); traceID != "" {
		return traceID
	}
	return stringField(objectAt(record["spanContext"]), "traceId")
}

func copilotSpanID(record map[string]any) string {
	if spanID := stringField(record, "spanId"); spanID != "" {
		return spanID
	}
	return stringField(objectAt(record["spanContext"]), "spanId")
}

func copilotBestSession(attrs map[string]any) (string, int) {
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
		if value := stringField(attrs, candidate.key); value != "" && candidate.priority > bestPriority {
			bestValue = value
			bestPriority = candidate.priority
		}
	}
	return bestValue, bestPriority
}

func copilotTimestamp(record map[string]any) (time.Time, bool) {
	for _, key := range []string{"endTime", "startTime", "hrTime", "_hrTime", "time"} {
		if timestamp, ok := timestampFromParts(record[key]); ok {
			return timestamp, true
		}
	}
	for _, key := range []string{"timestamp", "observedTimestamp"} {
		if timestamp, ok := parseTimestamp(record[key]); ok {
			return timestamp, true
		}
	}
	if raw := uintValue(record["timeUnixNano"]); raw > 0 {
		return time.UnixMilli(int64(raw / 1_000_000)), true
	}
	return time.Time{}, false
}

func copilotDedupKey(source copilotSource, record, attrs map[string]any, traceID, sessionID string, timestamp time.Time, index int) string {
	spanID := copilotSpanID(record)
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
		turnIndex := uintField(attrs, "turn.index", "copilot_chat.turn.index")
		turn := fmt.Sprintf("idx-%d", index)
		if turnIndex > 0 {
			turn = fmt.Sprintf("%d", turnIndex)
		}
		if traceID != "" {
			return "agent-turn:" + traceID + ":" + turn
		}
		return "agent-turn:" + sessionID + ":" + turn + fmt.Sprintf(":%d", index)
	default:
		return fmt.Sprintf("%s:%d", filepath.Base(copilotSourceName(source)), index)
	}
}

func copilotSourceName(source copilotSource) string {
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

type copilotSets struct {
	chatTraces         map[string]bool
	inferenceTraces    map[string]bool
	agentTurnTraces    map[string]bool
	chatResponses      map[string]bool
	inferenceResponses map[string]bool
	agentTurnResponses map[string]bool
}

func copilotCandidateSets(candidates []copilotCandidate) copilotSets {
	sets := copilotSets{
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

func shouldEmitCopilot(candidate copilotCandidate, sets copilotSets) bool {
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
