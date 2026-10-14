package codex

import (
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Prompts in a codex rollout.
//
// Codex brackets every turn with a task_started and a task_complete (or
// turn_aborted when it is interrupted), both carrying the turn_id. The start
// is a prompt's start half; the end carries what codex measured itself — the
// turn's duration and its time to first token — and is the end half. Older
// rollouts end turns without those timings; the end is still the end.
//
// Neither half carries anything the user typed.
//
// A thread another thread spawned emits no prompts. Codex opens such a
// rollout by copying the parent's whole history into it — every one of the
// parent's turns, under the parent's turn ids, rewritten with the spawn's
// timestamps — so its turns are the parent's prompts a second time, and
// which copy reached the server first would decide whether a question
// someone asked counted as one. Its own work runs in a session of its own,
// which no prompt's span reaches either way.

// promptStart records where a turn began.
func (state *fileState) promptStart(timestamp, turnID string) []usage.Entry {
	if state.subagent {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || turnID == "" {
		return nil
	}
	if state.turns == nil {
		state.turns = make(map[string]bool)
	}
	state.turns[turnID] = true
	entry := state.entry(usage.EventKindPrompt, turnID, at)
	entry.Prompt = &usage.PromptTurn{PromptID: turnID}
	return []usage.Entry{entry}
}

// promptEnd records how a turn this file started ended. An end with no start
// would be a row the server can never place, and is not emitted.
func (state *fileState) promptEnd(timestamp string, payload eventPayload) []usage.Entry {
	if !state.turns[payload.TurnID] {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil
	}
	outcome := usage.PromptCompleted
	switch {
	case payload.Type == "turn_aborted":
		outcome = usage.PromptAborted
	case len(payload.Error) > 0 && string(payload.Error) != "null":
		outcome = usage.PromptFailed
	}
	entry := state.entry(usage.EventKindPromptEnd, payload.TurnID, at)
	entry.Prompt = &usage.PromptTurn{
		PromptID:     payload.TurnID,
		Outcome:      outcome,
		DurationMS:   wholeMillis(payload.DurationMS),
		FirstTokenMS: wholeMillis(payload.FirstTokenMS),
	}
	return []usage.Entry{entry}
}

// wholeMillis keeps a reported timing as whole milliseconds; nil when codex
// reported none or reported nonsense.
func wholeMillis(ms *float64) *int64 {
	if ms == nil || *ms < 0 {
		return nil
	}
	whole := int64(*ms)
	return &whole
}
