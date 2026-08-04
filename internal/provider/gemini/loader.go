package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	files := make([]string, 0)
	for _, root := range paths {
		files = append(files, agentdata.CollectExt(root, ".json")...)
		files = append(files, agentdata.CollectExt(root, ".jsonl")...)
	}
	sort.Strings(files)
	files = agentdata.FilterFiles(agentdata.UniqueStrings(files), filter)

	resolver := newProjectResolver()
	entries := make([]usage.Entry, 0)
	for _, file := range files {
		var fileEntries []usage.Entry
		var err error
		if strings.EqualFold(filepath.Ext(file), ".jsonl") {
			fileEntries, err = parseJSONLFile(file, resolver)
		} else {
			fileEntries, err = parseJSONFile(file, resolver)
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

type tokens struct {
	input    uint64
	output   uint64
	cached   uint64
	thoughts uint64
	tool     uint64
	total    uint64
	hasTotal bool
}

// projectIdentity is the project a session file belongs to. Gemini CLI keys
// its data by slug directory (~/.gemini/tmp/<slug>/), so identity is resolved
// from the slug, not from the transcript contents.
type projectIdentity struct {
	project     string
	projectPath string
}

// projectResolver maps slug directories to projects. The slug directory may
// carry a .project_root file with the workspace path; otherwise the registry
// at <gemini-home>/projects.json maps workspace paths to slugs.
type projectResolver struct {
	identities map[string]projectIdentity
	registries map[string]map[string]string
}

func newProjectResolver() *projectResolver {
	return &projectResolver{
		identities: make(map[string]projectIdentity),
		registries: make(map[string]map[string]string),
	}
}

func (r *projectResolver) resolve(sessionFile string) projectIdentity {
	slugDir := slugDirOf(sessionFile)
	if identity, ok := r.identities[slugDir]; ok {
		return identity
	}
	identity := r.resolveSlugDir(slugDir)
	r.identities[slugDir] = identity
	return identity
}

// slugDirOf returns the per-project data directory a session file lives in:
// <root>/<slug>/chats/session-*.json(l) belongs to <root>/<slug>.
func slugDirOf(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "chats" {
		return filepath.Dir(dir)
	}
	return dir
}

func (r *projectResolver) resolveSlugDir(slugDir string) projectIdentity {
	cwd := readProjectRoot(filepath.Join(slugDir, ".project_root"))
	if cwd == "" {
		cwd = r.registryLookup(slugDir)
	}
	if path, name, ok := usage.ProjectFromCWD(cwd); ok {
		return projectIdentity{project: name, projectPath: path}
	}
	// A readable slug is the sanitized project name; a hash slug says nothing.
	if slug := filepath.Base(slugDir); !looksLikeHash(slug) {
		return projectIdentity{project: slug}
	}
	return projectIdentity{project: usage.UnknownProject}
}

func readProjectRoot(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

// registryLookup finds the workspace path for a slug in projects.json, which
// lives next to the tmp directory: <gemini-home>/projects.json maps workspace
// path -> slug. Iteration is sorted so a slug mapped from several paths always
// resolves identically.
func (r *projectResolver) registryLookup(slugDir string) string {
	registryPath := filepath.Join(filepath.Dir(filepath.Dir(slugDir)), "projects.json")
	registry, ok := r.registries[registryPath]
	if !ok {
		registry = loadProjectRegistry(registryPath)
		r.registries[registryPath] = registry
	}
	return registry[filepath.Base(slugDir)]
}

func loadProjectRegistry(path string) map[string]string {
	record, err := agentdata.ReadJSONObject(path)
	if err != nil || record == nil {
		return nil
	}
	projects := agentdata.ObjectAt(record["projects"])
	if projects == nil {
		return nil
	}
	cwds := make([]string, 0, len(projects))
	for cwd := range projects {
		cwds = append(cwds, cwd)
	}
	sort.Strings(cwds)
	bySlug := make(map[string]string, len(projects)*2)
	for _, cwd := range cwds {
		slug := agentdata.StringValue(projects[cwd])
		if cwd == "" || slug == "" {
			continue
		}
		if _, exists := bySlug[slug]; !exists {
			bySlug[slug] = cwd
		}
		// Older Gemini CLI versions named the slug directory
		// sha256(workspace path) instead of the sanitized project name.
		hashed := sha256Hex(cwd)
		if _, exists := bySlug[hashed]; !exists {
			bySlug[hashed] = cwd
		}
	}
	return bySlug
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func looksLikeHash(slug string) bool {
	if len(slug) < 32 {
		return false
	}
	for _, r := range slug {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			return false
		}
	}
	return true
}

// messageLog replays a session's message stream. Gemini CLI rewrites history
// in place — a message record with a known id supersedes the earlier version,
// and "$set" checkpoint lines repeat the whole array — so messages are keyed
// by id, last write wins, first-seen order preserved. Checkpoints never clear
// the log: a message dropped by a later checkpoint or "$rewindTo" still
// consumed tokens.
type messageLog struct {
	records []loggedMessage
	index   map[string]int
}

type loggedMessage struct {
	record map[string]any
	line   int
}

func newMessageLog() *messageLog {
	return &messageLog{index: make(map[string]int)}
}

func (l *messageLog) upsert(record map[string]any, line int) {
	if agentdata.StringField(record, "type") != "gemini" {
		return
	}
	logged := loggedMessage{record: record, line: line}
	if id := agentdata.StringField(record, "id"); id != "" {
		if i, ok := l.index[id]; ok {
			l.records[i] = logged
			return
		}
		l.index[id] = len(l.records)
	}
	l.records = append(l.records, logged)
}

func parseJSONFile(path string, resolver *projectResolver) ([]usage.Entry, error) {
	record, err := agentdata.ReadJSONObject(path)
	if err != nil || record == nil {
		return nil, err
	}
	identity := resolver.resolve(path)
	fallback := agentdata.FileModifiedTime(path)
	sessionID := agentdata.FirstStringField(record, "sessionId", "session_id")
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	sessionTimestamp := firstTimestamp(record, fallback, "startTime", "lastUpdated", "timestamp", "created_at")

	log := newMessageLog()
	if messages := agentdata.ArrayAt(record["messages"]); len(messages) > 0 {
		for index, raw := range messages {
			log.upsert(agentdata.ObjectAt(raw), index+1)
		}
		return messageEntries(log, path, "", identity, sessionID, sessionTimestamp), nil
	}
	log.upsert(record, 1)
	if len(log.records) > 0 {
		return messageEntries(log, path, "", identity, sessionID, fallback), nil
	}
	return statsEntries(recordStats(record), path, 1, agentdata.StringField(record, "model"), identity, sessionID, sessionTimestamp), nil
}

func parseJSONLFile(path string, resolver *projectResolver) ([]usage.Entry, error) {
	lines, err := agentdata.ReadJSONLines(path)
	if err != nil {
		return nil, err
	}
	identity := resolver.resolve(path)
	fallback := agentdata.FileModifiedTime(path)
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	currentModel := ""
	log := newMessageLog()
	entries := make([]usage.Entry, 0)
	for _, line := range lines {
		record := line.Value
		if value := agentdata.FirstStringField(record, "sessionId", "session_id"); value != "" {
			sessionID = value
		}
		if model := agentdata.StringField(record, "model"); model != "" {
			currentModel = model
		}
		// "$set" lines update session metadata; when they carry a messages
		// array they are a full-history checkpoint. Replaying every copy
		// through the log dedupes on message id.
		if set := agentdata.ObjectAt(record["$set"]); set != nil {
			if value := agentdata.FirstStringField(set, "sessionId", "session_id"); value != "" {
				sessionID = value
			}
			for _, raw := range agentdata.ArrayAt(set["messages"]) {
				log.upsert(agentdata.ObjectAt(raw), line.Line)
			}
			continue
		}
		if agentdata.StringField(record, "type") != "" {
			log.upsert(record, line.Line)
			continue
		}
		if stats := recordStats(record); stats != nil {
			timestamp := firstTimestamp(record, fallback, "timestamp")
			entries = append(entries, statsEntries(stats, path, line.Line, currentModel, identity, sessionID, timestamp)...)
		}
	}
	entries = append(entries, messageEntries(log, path, currentModel, identity, sessionID, fallback)...)
	return entries, nil
}

func messageEntries(log *messageLog, path, modelHint string, identity projectIdentity, sessionID string, fallback time.Time) []usage.Entry {
	entries := make([]usage.Entry, 0, len(log.records))
	for _, message := range log.records {
		if entry, ok := messageEntry(message.record, path, message.line, modelHint, identity, sessionID, fallback); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func messageEntry(record map[string]any, path string, line int, modelHint string, identity projectIdentity, sessionID string, fallback time.Time) (usage.Entry, bool) {
	tokens, ok := parseTokens(record["tokens"])
	if !ok {
		return usage.Entry{}, false
	}
	model := agentdata.StringField(record, "model")
	if model == "" {
		model = modelHint
	}
	timestamp := firstTimestamp(record, fallback, "timestamp", "created_at")
	entry, ok := buildGeminiEntry(path, line, model, identity, sessionID, timestamp, tokens, true, agentdata.StringField(record, "id"))
	if !ok {
		return usage.Entry{}, false
	}

	changes, candidates := toolCallData(record, identity.projectPath)
	for _, change := range changes {
		entry.ApplyFileChange(change)
	}
	if language := langdetect.Dominant(candidates); language != langdetect.Unknown {
		entry.Language = language
	}
	return entry, true
}

// toolCallData extracts the file modifications and language candidates from a
// gemini message's tool calls. write_file records the created content;
// replace/edit records the diff stat the CLI computed, falling back to the
// old/new string line counts when the stat is missing.
func toolCallData(record map[string]any, projectPath string) ([]usage.FileChange, []langdetect.Candidate) {
	var changes []usage.FileChange
	var candidates []langdetect.Candidate
	for _, raw := range agentdata.ArrayAt(record["toolCalls"]) {
		call := agentdata.ObjectAt(raw)
		if call == nil {
			continue
		}
		if status := agentdata.StringField(call, "status"); status != "" && status != "success" {
			continue
		}
		args := agentdata.ObjectAt(call["args"])
		candidates = append(candidates, languageCandidates(args)...)

		display := agentdata.ObjectAt(call["resultDisplay"])
		filePath := changePath(display, args, projectPath)
		if filePath == "" {
			continue
		}
		switch strings.ToLower(agentdata.StringField(call, "name")) {
		case "write_file", "writefile":
			changes = append(changes, usage.FileChange{
				Path:       filePath,
				LinesAdded: usage.CountLines(agentdata.StringField(args, "content")),
			})
		case "replace", "edit":
			added, removed := replaceLines(display, args)
			changes = append(changes, usage.FileChange{
				Path:         filePath,
				LinesAdded:   added,
				LinesRemoved: removed,
			})
		}
	}
	return changes, candidates
}

// languageCandidates mirrors the Claude provider's weighting: an explicit
// path argument names the file being worked on (weight 3); paths mentioned
// inside command or content text are weaker hints (weight 1).
func languageCandidates(args map[string]any) []langdetect.Candidate {
	candidates := make([]langdetect.Candidate, 0, len(args))
	for key, value := range args {
		lower := strings.ToLower(key)
		text := agentdata.StringValue(value)
		if text == "" {
			continue
		}
		switch {
		case strings.Contains(lower, "path") || strings.Contains(lower, "file"):
			candidates = append(candidates, langdetect.Candidate{Path: text, Weight: 3})
		case strings.Contains(lower, "command") || strings.Contains(lower, "content") || strings.Contains(lower, "query"):
			for _, path := range langdetect.PathsFromText(text) {
				candidates = append(candidates, langdetect.Candidate{Path: path, Weight: 1})
			}
		}
	}
	return candidates
}

func changePath(display, args map[string]any, projectPath string) string {
	path := agentdata.FirstStringField(display, "filePath", "fileName")
	if path == "" {
		path = agentdata.FirstStringField(args, "file_path", "filePath", "absolute_path")
	}
	return usage.ResolvePath(projectPath, path)
}

func replaceLines(display, args map[string]any) (uint64, uint64) {
	if diffStat := agentdata.ObjectAt(display["diffStat"]); diffStat != nil {
		return agentdata.UintField(diffStat, "model_added_lines"),
			agentdata.UintField(diffStat, "model_removed_lines")
	}
	return usage.CountLines(agentdata.StringField(args, "new_string")),
		usage.CountLines(agentdata.StringField(args, "old_string"))
}

func statsEntries(stats map[string]any, path string, line int, modelHint string, identity projectIdentity, sessionID string, timestamp time.Time) []usage.Entry {
	if stats == nil {
		return nil
	}
	if models := agentdata.ObjectAt(stats["models"]); models != nil {
		entries := make([]usage.Entry, 0)
		for model, raw := range models {
			data := agentdata.ObjectAt(raw)
			tokens, ok := parseTokens(data["tokens"])
			if !ok {
				continue
			}
			if entry, ok := buildGeminiEntry(path, line, model, identity, sessionID, timestamp, tokens, false, ""); ok {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 {
			return entries
		}
	}
	tokens, ok := parseTokens(stats)
	if !ok {
		return nil
	}
	model := modelHint
	if model == "" {
		model = "unknown"
	}
	entry, ok := buildGeminiEntry(path, line, model, identity, sessionID, timestamp, tokens, false, "")
	if !ok {
		return nil
	}
	return []usage.Entry{entry}
}

func buildGeminiEntry(path string, line int, model string, identity projectIdentity, sessionID string, timestamp time.Time, tokens tokens, direct bool, messageID string) (usage.Entry, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return usage.Entry{}, false
	}
	input, cacheRead := normalizeGeminiInput(tokens, direct)
	tokenUsage := usage.TokenUsage{
		InputTokens:           input + tokens.tool,
		OutputTokens:          tokens.output,
		CacheReadInputTokens:  cacheRead,
		ReasoningOutputTokens: tokens.thoughts,
	}
	if tokens.hasTotal {
		tokenUsage = usageprovider.ApplyTotalFallback(tokenUsage, tokens.total)
	} else if tokenUsage.TotalTokens == 0 {
		tokenUsage.TotalTokens = usageprovider.TotalUsage(tokenUsage)
	}
	if !usageprovider.NonZero(tokenUsage) {
		return usage.Entry{}, false
	}
	entry := usageprovider.BaseEntry(usage.ProviderGemini, timestamp, identity.project, identity.projectPath, sessionID, model, "Gemini CLI", tokenUsage)
	usageprovider.SetSource(&entry, path, line, 0, 0)
	// Session files are rewritten in place — a message's line and token
	// counts move as history is checkpointed — so like the Claude provider,
	// the entry is keyed on the message id alone. Only id-less records fall
	// back to source position.
	if messageID != "" {
		entry.ID = usage.StableID(string(usage.ProviderGemini), messageID)
	} else {
		entry.ID = usageprovider.StableEntryID(entry)
	}
	return entry, true
}

func parseTokens(raw any) (tokens, bool) {
	record := agentdata.ObjectAt(raw)
	if record == nil {
		return tokens{}, false
	}
	tokens := tokens{
		input:    agentdata.UintField(record, "input", "prompt", "input_tokens", "prompt_tokens"),
		output:   agentdata.UintField(record, "output", "candidates", "output_tokens", "candidates_tokens"),
		cached:   agentdata.UintField(record, "cached", "cached_tokens"),
		thoughts: agentdata.UintField(record, "thoughts", "reasoning", "thoughts_tokens", "reasoning_tokens"),
		tool:     agentdata.UintField(record, "tool", "tool_tokens"),
		total:    agentdata.UintField(record, "total", "total_tokens"),
	}
	tokens.hasTotal = tokens.total > 0
	return tokens, true
}

func normalizeGeminiInput(tokens tokens, direct bool) (uint64, uint64) {
	if !direct {
		cachedPortion := tokens.input
		if tokens.cached < cachedPortion {
			cachedPortion = tokens.cached
		}
		return tokens.input - cachedPortion, tokens.cached
	}
	inclusiveTotal := tokens.input + tokens.output + tokens.thoughts + tokens.tool
	exclusiveTotal := inclusiveTotal + tokens.cached
	if tokens.cached > 0 && tokens.hasTotal && tokens.total == inclusiveTotal && tokens.total != exclusiveTotal {
		cachedPortion := tokens.input
		if tokens.cached < cachedPortion {
			cachedPortion = tokens.cached
		}
		return tokens.input - cachedPortion, tokens.cached
	}
	return tokens.input, tokens.cached
}

func recordStats(record map[string]any) map[string]any {
	if stats := agentdata.ObjectAt(record["stats"]); stats != nil {
		return stats
	}
	result := agentdata.ObjectAt(record["result"])
	return agentdata.ObjectAt(result["stats"])
}

func firstTimestamp(record map[string]any, fallback time.Time, keys ...string) time.Time {
	for _, key := range keys {
		if timestamp, ok := agentdata.ParseTimestamp(record[key]); ok {
			return timestamp
		}
	}
	return fallback
}
