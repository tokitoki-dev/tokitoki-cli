package opencode

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// OpenCode stores everything in one SQLite database: sessions own messages,
// messages own parts. Tokens live on the message, the working directory lives
// on both the session and the message, and file edits live in the parts. All
// three tables are read together because an entry needs all three.
type session struct {
	directory string
	version   string
}

type message struct {
	id        string
	sessionID string
	created   int64
	data      map[string]any
	parts     []map[string]any
}

func loadEntries(paths []string, filter usage.FileFilter) ([]usage.Entry, error) {
	entriesByID := make(map[string]usage.Entry)
	for _, root := range paths {
		rootEntries, err := loadRoot(root, filter)
		if err != nil {
			return nil, err
		}
		for _, entry := range rootEntries {
			if entry.ID == "" {
				entry.ID = shared.StableEntryID(entry)
			}
			if _, exists := entriesByID[entry.ID]; !exists {
				entriesByID[entry.ID] = entry
			}
		}
	}
	entries := make([]usage.Entry, 0, len(entriesByID))
	for _, entry := range entriesByID {
		entries = append(entries, entry)
	}
	shared.SortEntries(entries)
	return entries, nil
}

func loadRoot(root string, filter usage.FileFilter) ([]usage.Entry, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil
	}
	if !info.IsDir() {
		switch strings.ToLower(filepath.Ext(root)) {
		case ".db":
			return loadDatabase(root)
		case ".json":
			return loadLegacyFile(root)
		}
		return nil, nil
	}

	entries := make([]usage.Entry, 0)
	seenIDs := make(map[string]bool)
	for _, dbPath := range dbPaths(root) {
		dbEntries, err := loadDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range dbEntries {
			if entry.ID != "" {
				seenIDs[entry.ID] = true
			}
			entries = append(entries, entry)
		}
	}

	// Older OpenCode releases wrote one JSON file per message instead of the
	// database. Their users keep those files, so keep reading them.
	files := shared.CollectExt(filepath.Join(root, "storage", "message"), ".json")
	sort.Strings(files)
	files = shared.FilterFiles(files, filter)
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if seenIDs[stableMessageID(stem)] {
			continue
		}
		legacy, err := loadLegacyFile(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range legacy {
			if entry.ID != "" && seenIDs[entry.ID] {
				continue
			}
			if entry.ID != "" {
				seenIDs[entry.ID] = true
			}
			entries = append(entries, entry)
		}
	}
	shared.SortEntries(entries)
	return entries, nil
}

// dbPaths lists the databases in a data root. OpenCode ships release
// channels side by side ("opencode.db", "opencode-nightly.db"), each a full
// database of its own.
func dbPaths(root string) []string {
	matches, err := filepath.Glob(filepath.Join(root, "opencode*.db"))
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(matches))
	for _, candidate := range matches {
		if shared.ExistingSQLiteFile(candidate) {
			paths = append(paths, candidate)
		}
	}
	sort.Strings(paths)
	return paths
}

func loadDatabase(path string) ([]usage.Entry, error) {
	db, err := shared.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	sessions := querySessions(db)
	messages := queryMessages(db)
	if len(messages) == 0 {
		return nil, nil
	}
	attachParts(db, messages)

	// Token counts on a message are the running context total, not that turn's
	// cost, so each entry bills the growth since the previous message of the
	// same session. Messages are ordered by creation time for that subtraction
	// to mean anything.
	bySession := make(map[string][]*message)
	for _, message := range messages {
		bySession[message.sessionID] = append(bySession[message.sessionID], message)
	}

	entries := make([]usage.Entry, 0, len(messages))
	for sessionID, sessionMessages := range bySession {
		sort.SliceStable(sessionMessages, func(i, j int) bool {
			if sessionMessages[i].created != sessionMessages[j].created {
				return sessionMessages[i].created < sessionMessages[j].created
			}
			return sessionMessages[i].id < sessionMessages[j].id
		})
		entries = append(entries, sessionEntries(path, sessions[sessionID], sessionMessages)...)
	}
	return entries, nil
}

func querySessions(db *sql.DB) map[string]session {
	sessions := make(map[string]session)
	rows, err := db.Query(`SELECT id, COALESCE(directory, ''), COALESCE(version, '') FROM session`)
	if err != nil {
		return sessions
	}
	defer rows.Close()
	for rows.Next() {
		var id, directory, version string
		if err := rows.Scan(&id, &directory, &version); err != nil {
			continue
		}
		sessions[id] = session{directory: directory, version: version}
	}
	return sessions
}

func queryMessages(db *sql.DB) []*message {
	rows, err := db.Query(`SELECT id, session_id, time_created, CAST(data AS TEXT) FROM message`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	messages := make([]*message, 0)
	for rows.Next() {
		var id, sessionID, data string
		var created int64
		if err := rows.Scan(&id, &sessionID, &created, &data); err != nil {
			continue
		}
		record := shared.DecodeJSONObjectString(data)
		if record == nil {
			continue
		}
		messages = append(messages, &message{
			id:        shared.FirstNonEmpty(shared.StringField(record, "id"), id),
			sessionID: shared.FirstNonEmpty(shared.StringField(record, "sessionID"), sessionID),
			created:   created,
			data:      record,
		})
	}
	return messages
}

func attachParts(db *sql.DB, messages []*message) {
	byID := make(map[string][]*message, len(messages))
	for _, message := range messages {
		byID[message.id] = append(byID[message.id], message)
	}

	rows, err := db.Query(`SELECT message_id, CAST(data AS TEXT) FROM part ORDER BY time_created ASC, id ASC`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, data string
		if err := rows.Scan(&messageID, &data); err != nil {
			continue
		}
		targets := byID[messageID]
		if len(targets) == 0 {
			continue
		}
		record := shared.DecodeJSONObjectString(data)
		if record == nil {
			continue
		}
		for _, target := range targets {
			target.parts = append(target.parts, record)
		}
	}
}

func sessionEntries(source string, session session, messages []*message) []usage.Entry {
	entries := make([]usage.Entry, 0, len(messages))
	var previous usage.TokenUsage
	for _, message := range messages {
		// User messages carry an all-zero token block. Letting one reset the
		// running counters would make the next assistant message look like a
		// fresh session and bill it for the whole context again.
		reported := tokens(message.data)
		if !shared.NonZero(reported) {
			continue
		}
		billed, ok := billTokens(reported, previous)
		previous = reported
		if !ok {
			continue
		}
		entry, ok := newEntry(source, session, message, billed)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// tokens reads a message's token block exactly as OpenCode wrote it.
// The fields do not all mean the same thing: input and the cache counters are
// running context totals that grow with every turn, while output and reasoning
// are what the model produced on this turn alone. billTokens is what
// turns this mixture into one turn's cost.
func tokens(record map[string]any) usage.TokenUsage {
	block := shared.ObjectAt(record["tokens"])
	if block == nil {
		return usage.TokenUsage{}
	}
	cache := shared.ObjectAt(block["cache"])
	return usage.TokenUsage{
		InputTokens:              shared.UintField(block, "input"),
		OutputTokens:             shared.UintField(block, "output"),
		ReasoningOutputTokens:    shared.UintField(block, "reasoning"),
		CacheCreationInputTokens: shared.UintField(cache, "write"),
		CacheReadInputTokens:     shared.UintField(cache, "read"),
	}
}

// billTokens charges a message for one turn: the growth of the running
// counters plus the per-turn counters as reported. The message's own "total"
// field is deliberately ignored — it sums the running counters, so it inherits
// their whole history and is not this turn's cost.
func billTokens(reported, previous usage.TokenUsage) (usage.TokenUsage, bool) {
	billed := usage.TokenUsage{
		InputTokens:              growth(reported.InputTokens, previous.InputTokens),
		CacheCreationInputTokens: growth(reported.CacheCreationInputTokens, previous.CacheCreationInputTokens),
		CacheReadInputTokens:     growth(reported.CacheReadInputTokens, previous.CacheReadInputTokens),
		OutputTokens:             reported.OutputTokens,
		ReasoningOutputTokens:    reported.ReasoningOutputTokens,
	}
	if !shared.NonZero(billed) {
		return usage.TokenUsage{}, false
	}
	return shared.ApplyTotalFallback(billed, 0), true
}

// growth returns how much a running counter advanced. A counter that shrank
// means the session was compacted or restarted, so the new value stands alone.
func growth(current, previous uint64) uint64 {
	if current <= previous {
		return current
	}
	return current - previous
}

func newEntry(source string, session session, message *message, tokens usage.TokenUsage) (usage.Entry, bool) {
	record := message.data
	model := shared.StringField(record, "modelID")
	if model == "" {
		return usage.Entry{}, false
	}

	timestamp := time.UnixMilli(message.created).UTC()
	if parsed, ok := shared.ParseTimestamp(shared.ObjectAt(record["time"])["created"]); ok {
		timestamp = parsed
	}

	// The message records the directory the agent actually ran in; the session
	// directory covers messages written before that field existed.
	cwd := shared.FirstNonEmpty(
		shared.StringField(shared.ObjectAt(record["path"]), "cwd"),
		shared.StringField(shared.ObjectAt(record["path"]), "root"),
		session.directory,
	)
	project, projectPath := usage.UnknownProject, ""
	if path, name, ok := usage.ProjectFromCWD(cwd); ok {
		project, projectPath = name, path
	}

	sessionID := shared.FirstNonEmpty(message.sessionID, usage.UnknownProject)
	entry := shared.BaseEntry(usage.ProviderOpenCode, timestamp, project, projectPath, sessionID, model, "OpenCode", tokens)
	shared.SetSource(&entry, source, 0, 0, 0)
	entry.ID = stableMessageID(message.id)
	if entry.ID == "" {
		entry.ID = shared.StableEntryID(entry)
	}

	for _, part := range message.parts {
		for _, change := range partChanges(part, cwd) {
			entry.ApplyFileChange(change)
		}
	}
	return entry, true
}

// partChanges extracts the file edits one message part performed.
// Only tool calls count: a "patch" part is a git snapshot of the whole
// worktree, which lists files nobody edited (.DS_Store, build output) and
// carries no line counts, so treating it as agent work invents writes.
func partChanges(part map[string]any, cwd string) []usage.FileChange {
	if shared.StringField(part, "type") != "tool" {
		return nil
	}

	state := shared.ObjectAt(part["state"])
	if shared.StringField(state, "status") != "completed" {
		return nil
	}
	input := shared.ObjectAt(state["input"])
	metadata := shared.ObjectAt(state["metadata"])

	switch shared.StringField(part, "tool") {
	case "edit":
		return editChanges(input, metadata, cwd)
	case "write":
		return writeChanges(input, metadata, cwd)
	case "patch", "apply_patch":
		return applyPatchChanges(input, metadata, cwd)
	default:
		return nil
	}
}

// editChanges prefers the diff OpenCode computed. Without it the
// replaced and replacing strings still bound the change.
func editChanges(input, metadata map[string]any, cwd string) []usage.FileChange {
	path := usage.ResolvePath(cwd, shared.StringField(input, "filePath"))

	if diff := shared.ObjectAt(metadata["filediff"]); diff != nil {
		added := shared.UintField(diff, "additions")
		removed := shared.UintField(diff, "deletions")
		if diffPath := shared.FirstNonEmpty(shared.StringField(diff, "filePath"), shared.StringField(diff, "file")); diffPath != "" {
			path = usage.ResolvePath(cwd, diffPath)
		}
		if added != 0 || removed != 0 {
			return []usage.FileChange{{Path: path, LinesAdded: added, LinesRemoved: removed}}
		}
	}

	added := usage.CountLines(shared.StringField(input, "newString"))
	removed := usage.CountLines(shared.StringField(input, "oldString"))
	if path == "" && added == 0 && removed == 0 {
		return nil
	}
	return []usage.FileChange{{Path: path, LinesAdded: added, LinesRemoved: removed}}
}

// writeChanges counts a write as adding its whole content. Overwriting
// an existing file replaces lines this record does not describe, so only the
// added side is known.
func writeChanges(input, metadata map[string]any, cwd string) []usage.FileChange {
	path := usage.ResolvePath(cwd, shared.FirstNonEmpty(
		shared.StringField(input, "filePath"),
		shared.StringField(metadata, "filepath"),
	))
	if path == "" {
		return nil
	}
	return []usage.FileChange{{Path: path, LinesAdded: usage.CountLines(shared.StringField(input, "content"))}}
}

// applyPatchChanges prefers the per-file summary OpenCode attaches and
// falls back to counting the raw patch envelope itself.
func applyPatchChanges(input, metadata map[string]any, cwd string) []usage.FileChange {
	if files, ok := metadata["files"].([]any); ok && len(files) > 0 {
		changes := make([]usage.FileChange, 0, len(files))
		for _, raw := range files {
			file := shared.ObjectAt(raw)
			if file == nil {
				continue
			}
			path := usage.ResolvePath(cwd, shared.FirstNonEmpty(shared.StringField(file, "filePath"), shared.StringField(file, "file")))
			if path == "" {
				continue
			}
			changes = append(changes, usage.FileChange{
				Path:         path,
				LinesAdded:   shared.UintField(file, "additions"),
				LinesRemoved: shared.UintField(file, "deletions"),
			})
		}
		if len(changes) > 0 {
			return changes
		}
	}

	patchText := shared.FirstNonEmpty(
		shared.StringField(input, "patchText"),
		shared.StringField(input, "patch"),
		shared.StringField(metadata, "patch"),
	)
	return parsePatchEnvelope(patchText, cwd)
}

// parsePatchEnvelope counts the added and removed lines of each file in
// an apply_patch envelope. One envelope can carry any number of files.
func parsePatchEnvelope(patchText, cwd string) []usage.FileChange {
	if strings.TrimSpace(patchText) == "" {
		return nil
	}

	changes := make([]usage.FileChange, 0)
	current := usage.FileChange{}
	flush := func() {
		if current.Path != "" {
			changes = append(changes, current)
		}
		current = usage.FileChange{}
	}

	for _, line := range strings.Split(patchText, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Move to: "):
			// A rename keeps the diff it already accumulated, under the new name.
			if current.Path != "" {
				current.Path = usage.ResolvePath(cwd, strings.TrimPrefix(line, "*** Move to: "))
			}
		case strings.HasPrefix(line, "*** Add File: "),
			strings.HasPrefix(line, "*** Update File: "),
			strings.HasPrefix(line, "*** Delete File: "):
			flush()
			current.Path = usage.ResolvePath(cwd, patchHeaderPath(line))
		case strings.HasPrefix(line, "*** End Patch"):
			flush()
		case current.Path == "":
		case strings.HasPrefix(line, "+"):
			current.LinesAdded++
		case strings.HasPrefix(line, "-"):
			current.LinesRemoved++
		}
	}
	flush()
	if len(changes) == 0 {
		return nil
	}
	return changes
}

func patchHeaderPath(line string) string {
	for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: "} {
		if path, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

// loadLegacyFile reads one message from the pre-database storage layout,
// where the message and its parts each lived in their own JSON file.
func loadLegacyFile(path string) ([]usage.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil, nil
	}

	message := &message{
		id:        shared.StringField(record, "id"),
		sessionID: shared.StringField(record, "sessionID"),
		data:      record,
		parts:     readLegacyParts(path, shared.StringField(record, "id")),
	}
	// One file holds one message, so there is no predecessor to subtract from:
	// billing against a zero baseline charges it for everything it reports.
	tokens, ok := billTokens(tokens(record), usage.TokenUsage{})
	if !ok {
		return nil, nil
	}
	entry, ok := newEntry(path, session{}, message, tokens)
	if !ok {
		return nil, nil
	}
	entry.SourceLine = 1
	return []usage.Entry{entry}, nil
}

// readLegacyParts loads the part files stored alongside a legacy
// message: storage/message/<session>/<message>.json pairs with
// storage/part/<message>/*.json.
func readLegacyParts(messagePath, messageID string) []map[string]any {
	if messageID == "" {
		return nil
	}
	storage := filepath.Dir(filepath.Dir(filepath.Dir(messagePath)))
	files := shared.CollectExt(filepath.Join(storage, "part", messageID), ".json")
	sort.Strings(files)

	parts := make([]map[string]any, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			continue
		}
		parts = append(parts, record)
	}
	return parts
}

// stableMessageID keys an entry to the message id OpenCode assigned.
// The id travels with the message no matter which database file or storage
// layout holds it, so re-ingesting the same message never double-counts.
func stableMessageID(messageID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	return usage.StableID("opencode", strings.TrimSpace(messageID))
}
