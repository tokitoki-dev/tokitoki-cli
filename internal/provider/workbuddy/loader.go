package workbuddy

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// fallbackModel is WorkBuddy's auto-router placeholder, used when neither the
// record nor settings.json names a model.
const fallbackModel = "auto"

// WorkBuddy (Tencent's Claude Code fork) writes session transcripts under
// <root>/projects/<encoded-cwd>/<session>.jsonl, with sub-agent traffic
// nested at <session>/subagents/<agent>.jsonl. Every LLM round-trip carries
// providerData.rawUsage — on function_call records as well as assistant
// messages — so usage is aggregated from any record that has it, not by
// record type.
func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	entries := make([]usage.Entry, 0)
	seen := make(map[string]bool)
	for _, root := range paths {
		files := agentdata.CollectExt(filepath.Join(root, "projects"), ".jsonl")
		files = agentdata.FilterFiles(files, filter)
		model := settingsModel(root)
		for _, file := range files {
			fileEntries, err := parseSessionFile(file, model)
			if err != nil {
				return nil, err
			}
			for _, entry := range fileEntries {
				if seen[entry.ID] {
					continue
				}
				seen[entry.ID] = true
				entries = append(entries, entry)
			}
		}
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

func parseSessionFile(path, settingsModel string) ([]usage.Entry, error) {
	lines, err := agentdata.ReadJSONLines(path)
	if err != nil {
		return nil, err
	}
	fileSessionID := sessionIDFromPath(path)
	fallbackTime := agentdata.FileModifiedTime(path)

	// Pass 1: build one entry per usage-carrying record and index the first
	// entry of each assistant turn by messageId.
	entries := make([]usage.Entry, 0, len(lines))
	entryLines := make([]int, 0, len(lines))
	entryByMid := make(map[string]int)
	for _, line := range lines {
		providerData := agentdata.ObjectAt(line.Value["providerData"])
		rawUsage := agentdata.ObjectAt(providerData["rawUsage"])
		if rawUsage == nil {
			continue
		}
		tokens := normalizeTokens(rawUsage)
		if !usageprovider.NonZero(tokens) {
			continue
		}

		timestamp, ok := agentdata.ParseTimestamp(line.Value["timestamp"])
		if !ok {
			timestamp = fallbackTime
		}

		project := usage.UnknownProject
		projectPath := ""
		if path, name, ok := usage.ProjectFromCWD(agentdata.StringField(line.Value, "cwd")); ok {
			projectPath = path
			project = name
		}

		sessionID := agentdata.FirstNonEmpty(agentdata.StringField(line.Value, "sessionId"), fileSessionID)
		// providerData.model is the model the auto-router actually picked;
		// requestModelId is usually the "auto" placeholder.
		model := agentdata.FirstNonEmpty(
			agentdata.StringField(providerData, "model"),
			agentdata.StringField(providerData, "requestModelId"),
			agentdata.StringField(line.Value, "model"),
			settingsModel,
		)

		entry := usageprovider.BaseEntry(usage.ProviderWorkbuddy, timestamp, project, projectPath, sessionID, model, "", tokens)
		usageprovider.SetSource(&entry, path, line.Line, line.Start, line.End)
		entry.ID = entryID(entry, providerData, line.Value)
		if mid := agentdata.StringField(providerData, "messageId"); mid != "" {
			if _, exists := entryByMid[mid]; !exists {
				entryByMid[mid] = len(entries)
			}
		}
		entries = append(entries, entry)
		entryLines = append(entryLines, line.Line)
	}
	if len(entries) == 0 {
		return entries, nil
	}

	// Pass 2: route file diffs and language signals onto entries. A record's
	// messageId names its assistant turn exactly; a record whose turn produced
	// no usage entry (interrupted turn, parallel tool calls) falls back to the
	// nearest preceding entry, and anything before the first entry lands on it.
	candidates := make([][]langdetect.Candidate, len(entries))
	last, next := 0, 0
	for _, line := range lines {
		for next < len(entries) && entryLines[next] <= line.Line {
			last = next
			next++
		}
		target := last
		mid := agentdata.StringField(agentdata.ObjectAt(line.Value["providerData"]), "messageId")
		if index, ok := entryByMid[mid]; ok && mid != "" {
			target = index
		}
		if change, ok := fileChangeFromCall(line.Value); ok {
			entries[target].ApplyFileChange(change)
		}
		candidates[target] = append(candidates[target], languageCandidates(line.Value)...)
	}
	for i := range entries {
		entries[i].Language = usage.NormalizeLanguage(langdetect.Dominant(candidates[i]))
	}
	return entries, nil
}

// fileChangeFromCall turns a Write or Edit tool call into the diff it applies.
// WorkBuddy records no structured patch, so the tool arguments are the diff:
// a Write's content is all added lines and an Edit swaps old_string for
// new_string. Line counts come from the call, not the result — a rejected
// call overcounts slightly, which beats parsing every result blob.
func fileChangeFromCall(value map[string]any) (usage.FileChange, bool) {
	if agentdata.StringField(value, "type") != "function_call" {
		return usage.FileChange{}, false
	}
	arguments := agentdata.DecodeJSONObjectString(agentdata.StringField(value, "arguments"))
	if arguments == nil {
		return usage.FileChange{}, false
	}
	path := usage.ResolvePath(agentdata.StringField(value, "cwd"), agentdata.StringField(arguments, "file_path"))
	if path == "" {
		return usage.FileChange{}, false
	}
	switch agentdata.StringField(value, "name") {
	case "Write":
		return usage.FileChange{
			Path:       path,
			LinesAdded: usage.CountLines(agentdata.StringField(arguments, "content")),
		}, true
	case "Edit":
		return usage.FileChange{
			Path:         path,
			LinesAdded:   usage.CountLines(agentdata.StringField(arguments, "new_string")),
			LinesRemoved: usage.CountLines(agentdata.StringField(arguments, "old_string")),
		}, true
	}
	return usage.FileChange{}, false
}

// languageCandidates mines a record for programming-language signals the same
// way the Claude provider mines an assistant message: tool-call file paths
// weigh 3, free text weighs 1 per path it mentions. function_call arguments
// are WorkBuddy's tool_use blocks; message content carries the text blocks.
func languageCandidates(value map[string]any) []langdetect.Candidate {
	candidates := make([]langdetect.Candidate, 0)
	switch agentdata.StringField(value, "type") {
	case "function_call":
		arguments := agentdata.DecodeJSONObjectString(agentdata.StringField(value, "arguments"))
		for key, child := range arguments {
			lower := strings.ToLower(key)
			text := agentdata.StringValue(child)
			if text == "" {
				continue
			}
			if strings.Contains(lower, "file") || strings.Contains(lower, "path") {
				if langdetect.FromPath(text) != langdetect.Unknown {
					candidates = append(candidates, langdetect.Candidate{Path: text, Weight: 3})
					continue
				}
			}
			if strings.Contains(lower, "command") || strings.Contains(lower, "content") || strings.Contains(lower, "query") {
				for _, path := range langdetect.PathsFromText(text) {
					candidates = append(candidates, langdetect.Candidate{Path: path, Weight: 1})
				}
			}
		}
	case "message":
		for _, block := range agentdata.ArrayAt(value["content"]) {
			text := agentdata.StringField(agentdata.ObjectAt(block), "text")
			for _, path := range langdetect.PathsFromText(text) {
				candidates = append(candidates, langdetect.Candidate{Path: path, Weight: 1})
			}
		}
		if text := agentdata.StringValue(value["content"]); text != "" {
			for _, path := range langdetect.PathsFromText(text) {
				candidates = append(candidates, langdetect.Candidate{Path: path, Weight: 1})
			}
		}
	}
	return candidates
}

// normalizeTokens converts WorkBuddy's OpenAI-shaped rawUsage into the
// normalized breakdown. prompt_tokens is the FULL prompt — cache reads,
// cache writes, and genuinely-new input — and the cache split is mirrored in
// Anthropic-style or DeepSeek-style fields depending on which upstream the
// auto-router picked, so each cache bucket takes the largest mirror.
// completion_tokens includes reasoning (total == prompt + completion).
func normalizeTokens(rawUsage map[string]any) usage.TokenUsage {
	prompt := agentdata.UintField(rawUsage, "prompt_tokens")
	completion := agentdata.UintField(rawUsage, "completion_tokens")
	promptDetails := agentdata.ObjectAt(rawUsage["prompt_tokens_details"])
	completionDetails := agentdata.ObjectAt(rawUsage["completion_tokens_details"])

	cacheRead := max(
		agentdata.UintField(rawUsage, "cache_read_input_tokens"),
		agentdata.UintField(promptDetails, "cached_tokens"),
		agentdata.UintField(rawUsage, "prompt_cache_hit_tokens"),
	)
	cacheCreation := max(
		agentdata.UintField(rawUsage, "cache_creation_input_tokens"),
		agentdata.UintField(rawUsage, "prompt_cache_write_tokens"),
	)
	input := uint64(0)
	if cached := cacheRead + cacheCreation; prompt > cached {
		input = prompt - cached
	}
	reasoning := min(completion, max(
		agentdata.UintField(completionDetails, "reasoning_tokens"),
		agentdata.UintField(rawUsage, "completion_thinking_tokens"),
	))
	output := completion - reasoning

	return usage.TokenUsage{
		InputTokens:              input,
		OutputTokens:             output,
		CacheCreationInputTokens: cacheCreation,
		CacheReadInputTokens:     cacheRead,
		ReasoningOutputTokens:    reasoning,
		TotalTokens:              input + output + cacheCreation + cacheRead + reasoning,
	}
}

// entryID keys on the response-level providerData.messageId plus the token
// counts. The messageId is shared by every record of one logical assistant
// turn and can be replayed across session files, so anything file- or
// position-specific in the key would double count a mirrored record. But one
// turn can also contain several REAL API calls under the same messageId
// (observed: a Write call and its continuation, seconds apart, each with its
// own growing prompt) — the token counts are what tell a mirror from a
// genuine second call, so they complete the key.
func entryID(entry usage.Entry, providerData, value map[string]any) string {
	messageID := agentdata.StringField(providerData, "messageId")
	if messageID == "" {
		messageID = agentdata.FirstStringField(value, "uuid", "id")
	}
	if messageID != "" {
		tokens := entry.Usage
		return usage.StableID(
			string(usage.ProviderWorkbuddy),
			messageID,
			strconv.FormatUint(tokens.InputTokens, 10),
			strconv.FormatUint(tokens.OutputTokens, 10),
			strconv.FormatUint(tokens.CacheCreationInputTokens, 10),
			strconv.FormatUint(tokens.CacheReadInputTokens, 10),
			strconv.FormatUint(tokens.ReasoningOutputTokens, 10),
		)
	}
	return usageprovider.StableEntryID(entry)
}

// sessionIDFromPath derives the session id from the file's location under
// projects/: <project>/<session>.jsonl for main sessions and
// <project>/<session>/subagents/<agent>.jsonl for sub-agents.
func sessionIDFromPath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		return filepath.Base(filepath.Dir(dir))
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if stem == "" {
		return "unknown"
	}
	return stem
}

func settingsModel(root string) string {
	settings, err := agentdata.ReadJSONObject(filepath.Join(root, "settings.json"))
	if err != nil || settings == nil {
		return fallbackModel
	}
	if model := agentdata.StringField(settings, "model"); model != "" {
		return model
	}
	return fallbackModel
}
