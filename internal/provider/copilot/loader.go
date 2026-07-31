package copilot

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, shared.CollectExt(root, ".jsonl")...)
	}
	sort.Strings(files)
	files = shared.FilterFiles(shared.UniqueStrings(files), filter)

	entries := make([]usage.Entry, 0)
	for _, file := range files {
		fileEntries, err := parseOTELFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	shared.SortEntries(entries)
	return entries, nil
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
	lines, err := shared.ReadJSONLines(path, `"attributes"`)
	if err != nil {
		return nil, err
	}
	contexts := traceContexts(lines)
	fallback := shared.FileModifiedTime(path)
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
		entry := shared.BaseEntry(
			usage.ProviderCopilot,
			candidate.timestamp,
			"copilot",
			"GitHub Copilot CLI",
			candidate.sessionID,
			candidate.model,
			"GitHub Copilot CLI",
			candidate.tokens,
		)
		shared.SetSource(&entry, candidate.sourceFile, candidate.sourceLine, candidate.sourceStart, candidate.sourceEnd)
		entry.ID = shared.StableEntryID(entry, candidate.dedupKey)
		entries = append(entries, entry)
	}
	return entries, nil
}

func traceContexts(lines []shared.LineJSON) map[string]traceContext {
	contexts := make(map[string]traceContext)
	for _, line := range lines {
		record := line.Value
		traceID := traceID(record)
		if traceID == "" {
			continue
		}
		attrs := shared.ObjectAt(record["attributes"])
		if attrs == nil {
			continue
		}
		context := contexts[traceID]
		if context.model == "" {
			context.model = shared.FirstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
		}
		if sessionID, priority := bestSession(attrs); sessionID != "" && priority > context.sessionIDPriority {
			context.sessionID = sessionID
			context.sessionIDPriority = priority
		}
		contexts[traceID] = context
	}
	return contexts
}

func recordCandidate(path string, line shared.LineJSON, index int, fallback time.Time, contexts map[string]traceContext) (candidate, bool) {
	record := line.Value
	attrs := shared.ObjectAt(record["attributes"])
	if attrs == nil {
		return candidate{}, false
	}
	source, ok := recordSource(record, attrs)
	if !ok {
		return candidate{}, false
	}
	input := shared.UintField(attrs, "gen_ai.usage.input_tokens")
	cacheRead := shared.UintField(attrs, "gen_ai.usage.cache_read.input_tokens")
	if cacheRead <= input {
		input -= cacheRead
	} else {
		input = 0
	}
	tokens := usage.TokenUsage{
		InputTokens:              input,
		OutputTokens:             shared.UintField(attrs, "gen_ai.usage.output_tokens"),
		CacheCreationInputTokens: shared.UintField(attrs, "gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens"),
		CacheReadInputTokens:     cacheRead,
		ReasoningOutputTokens:    shared.UintField(attrs, "gen_ai.usage.reasoning.output_tokens", "gen_ai.usage.reasoning_tokens"),
	}
	tokens = shared.ApplyTotalFallback(tokens, shared.UintField(attrs, "gen_ai.usage.total_tokens", "gen_ai.usage.total.token_count"))
	if !shared.NonZero(tokens) {
		return candidate{}, false
	}
	traceID := traceID(record)
	context := contexts[traceID]
	model := shared.FirstStringField(attrs, "gen_ai.response.model", "gen_ai.request.model")
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
	responseID := shared.StringField(attrs, "gen_ai.response.id")
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
	if shared.StringField(record, "type") == "span" {
		return true
	}
	if shared.StringField(record, "name") == "" {
		return false
	}
	return shared.StringField(record, "spanId") != "" ||
		shared.StringField(record, "traceId") != "" ||
		record["startTime"] != nil ||
		record["endTime"] != nil ||
		record["duration"] != nil ||
		record["kind"] != nil
}

func isChatSpan(record, attrs map[string]any) bool {
	return isSpan(record) &&
		(shared.StringField(attrs, "gen_ai.operation.name") == "chat" ||
			strings.HasPrefix(shared.StringField(record, "name"), "chat "))
}

func isAgentSummarySpan(record, attrs map[string]any) bool {
	return isSpan(record) &&
		(shared.StringField(attrs, "gen_ai.operation.name") == "invoke_agent" ||
			strings.HasPrefix(shared.StringField(record, "name"), "invoke_agent "))
}

func isInferenceLog(record, attrs map[string]any) bool {
	return !isSpan(record) &&
		(shared.StringField(attrs, "event.name") == "gen_ai.client.inference.operation.details" ||
			strings.HasPrefix(spanBody(record), "GenAI inference:"))
}

func isAgentTurnLog(record, attrs map[string]any) bool {
	return !isSpan(record) &&
		(shared.StringField(attrs, "event.name") == "copilot_chat.agent.turn" ||
			strings.HasPrefix(spanBody(record), "copilot_chat.agent.turn"))
}

func spanBody(record map[string]any) string {
	if body := shared.StringField(record, "body"); body != "" {
		return body
	}
	return shared.StringField(record, "_body")
}

func traceID(record map[string]any) string {
	if traceID := shared.StringField(record, "traceId"); traceID != "" {
		return traceID
	}
	return shared.StringField(shared.ObjectAt(record["spanContext"]), "traceId")
}

func spanID(record map[string]any) string {
	if spanID := shared.StringField(record, "spanId"); spanID != "" {
		return spanID
	}
	return shared.StringField(shared.ObjectAt(record["spanContext"]), "spanId")
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
		if value := shared.StringField(attrs, candidate.key); value != "" && candidate.priority > bestPriority {
			bestValue = value
			bestPriority = candidate.priority
		}
	}
	return bestValue, bestPriority
}

func timestamp(record map[string]any) (time.Time, bool) {
	for _, key := range []string{"endTime", "startTime", "hrTime", "_hrTime", "time"} {
		if timestamp, ok := shared.TimestampFromParts(record[key]); ok {
			return timestamp, true
		}
	}
	for _, key := range []string{"timestamp", "observedTimestamp"} {
		if timestamp, ok := shared.ParseTimestamp(record[key]); ok {
			return timestamp, true
		}
	}
	if raw := shared.UintValue(record["timeUnixNano"]); raw > 0 {
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
		turnIndex := shared.UintField(attrs, "turn.index", "copilot_chat.turn.index")
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
