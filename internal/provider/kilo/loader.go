package kilo

import (
	"database/sql"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"
	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Kilo's engine is a fork of OpenCode, so the layout is the same one SQLite
// database: sessions own messages, messages own parts, tokens live on the
// message and file edits live in the parts. The fork changed what the numbers
// mean, though: a Kilo message reports what its one API request consumed, not
// OpenCode's running context totals, so entries here bill each message as
// reported with no subtraction. The session rows sum their messages' fields
// verbatim, which is the fork's own word on that semantics.
//
// The Kilo CLI and the Kilo VSCode extension run this same engine against
// this same database, and no table records which of the two wrote a session.
// Both are therefore one agent: "kilo".
type session struct {
	directory string
	model     string
}

type message struct {
	id        string
	sessionID string
	created   int64
	data      map[string]any
	parts     []map[string]any
}

func loadEntries(paths []string) ([]usage.Entry, error) {
	entriesByID := make(map[string]usage.Entry)
	for _, dbPath := range agentdb.SqliteDBPaths(paths, "kilo.db", nil) {
		dbEntries, err := loadDatabase(dbPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range dbEntries {
			if _, exists := entriesByID[entry.ID]; !exists {
				entriesByID[entry.ID] = entry
			}
		}
	}
	entries := make([]usage.Entry, 0, len(entriesByID))
	for _, entry := range entriesByID {
		entries = append(entries, entry)
	}
	usageprovider.SortEntries(entries)
	return entries, nil
}

func loadDatabase(path string) ([]usage.Entry, error) {
	db, err := agentdb.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	messages := queryMessages(db)
	if len(messages) == 0 {
		return nil, nil
	}
	sessions := querySessions(db)
	attachParts(db, messages)

	entries := make([]usage.Entry, 0, len(messages))
	for _, message := range messages {
		if entry, ok := newEntry(path, sessions[message.sessionID], message); ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func querySessions(db *sql.DB) map[string]session {
	sessions := make(map[string]session)
	// Early Kilo builds predate the model column; losing it must not lose the
	// directory too.
	rows, err := db.Query(`SELECT id, COALESCE(directory, ''), COALESCE(model, '') FROM session`)
	if err != nil {
		rows, err = db.Query(`SELECT id, COALESCE(directory, ''), '' FROM session`)
	}
	if err != nil {
		return sessions
	}
	defer rows.Close()
	for rows.Next() {
		var id, directory, model string
		if err := rows.Scan(&id, &directory, &model); err != nil {
			continue
		}
		// The session's model is a JSON object, unlike the message's flat
		// modelID: {"id":"kilo-auto/free","providerID":"kilo",...}.
		block := agentdata.DecodeJSONObjectString(model)
		sessions[id] = session{
			directory: directory,
			model: agentdata.FirstNonEmpty(
				agentdata.StringField(block, "id"),
				agentdata.StringField(block, "modelID"),
			),
		}
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
		record := agentdata.DecodeJSONObjectString(data)
		if record == nil {
			continue
		}
		messages = append(messages, &message{
			id:        agentdata.FirstNonEmpty(agentdata.StringField(record, "id"), id),
			sessionID: agentdata.FirstNonEmpty(agentdata.StringField(record, "sessionID"), sessionID),
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
		record := agentdata.DecodeJSONObjectString(data)
		if record == nil {
			continue
		}
		for _, target := range targets {
			target.parts = append(target.parts, record)
		}
	}
}

// tokens bills a message for what its API request consumed, exactly as
// reported. User messages carry no token block and drop out as all-zero.
func tokens(record map[string]any) usage.TokenUsage {
	block := agentdata.ObjectAt(record["tokens"])
	if block == nil {
		return usage.TokenUsage{}
	}
	cache := agentdata.ObjectAt(block["cache"])
	billed := usage.TokenUsage{
		InputTokens:              agentdata.UintField(block, "input"),
		OutputTokens:             agentdata.UintField(block, "output"),
		ReasoningOutputTokens:    agentdata.UintField(block, "reasoning"),
		CacheCreationInputTokens: agentdata.UintField(cache, "write"),
		CacheReadInputTokens:     agentdata.UintField(cache, "read"),
	}
	return usageprovider.ApplyTotalFallback(billed, agentdata.UintField(block, "total"))
}

// modelName resolves which model answered a turn: the message's flat modelID,
// its nested model object, or the model the session was started with. An
// entry keeps its tokens even when none of them is set — losing a turn's
// usage is worse than not knowing its model.
func modelName(record map[string]any, session session) string {
	return agentdata.FirstNonEmpty(
		agentdata.StringField(record, "modelID"),
		agentdata.StringField(agentdata.ObjectAt(record["model"]), "modelID"),
		agentdata.StringField(agentdata.ObjectAt(record["model"]), "id"),
		session.model,
	)
}

func newEntry(source string, session session, message *message) (usage.Entry, bool) {
	record := message.data
	billed := tokens(record)
	if !usageprovider.NonZero(billed) {
		return usage.Entry{}, false
	}

	timestamp := time.UnixMilli(message.created).UTC()
	if parsed, ok := agentdata.ParseTimestamp(agentdata.ObjectAt(record["time"])["created"]); ok {
		timestamp = parsed
	}

	// The message records the directory the agent actually ran in; the session
	// directory covers messages that predate the field.
	cwd := agentdata.FirstNonEmpty(
		agentdata.StringField(agentdata.ObjectAt(record["path"]), "cwd"),
		agentdata.StringField(agentdata.ObjectAt(record["path"]), "root"),
		session.directory,
	)
	project, projectPath := usage.UnknownProject, ""
	if path, name, ok := usage.ProjectFromCWD(cwd); ok {
		project, projectPath = name, path
	}

	sessionID := agentdata.FirstNonEmpty(message.sessionID, usage.UnknownProject)
	entry := usageprovider.BaseEntry(usage.ProviderKilo, timestamp, project, projectPath, sessionID, modelName(record, session), "Kilo", billed)
	usageprovider.SetSource(&entry, source, 0, 0, 0)
	entry.ID = stableMessageID(message.id)
	if entry.ID == "" {
		entry.ID = usageprovider.StableEntryID(entry)
	}

	candidates := make([]langdetect.Candidate, 0)
	for _, part := range message.parts {
		for _, change := range partChanges(part, cwd) {
			entry.ApplyFileChange(change)
		}
		candidates = append(candidates, languageCandidates(part, cwd)...)
	}
	entry.Language = usage.NormalizeLanguage(langdetect.Dominant(candidates))
	return entry, true
}

// Weights for the paths a turn touched. Writing a file says far more about
// what is being worked on than reading one, and a path merely mentioned in a
// shell command says least of all.
const (
	writeWeight = 4
	readWeight  = 2
	textWeight  = 1
)

// languageCandidates collects the file paths one part touched, weighted by
// how strongly each says what language the turn was spent on.
func languageCandidates(part map[string]any, cwd string) []langdetect.Candidate {
	if agentdata.StringField(part, "type") != "tool" {
		return nil
	}
	state := agentdata.ObjectAt(part["state"])
	if agentdata.StringField(state, "status") != "completed" {
		return nil
	}
	input := agentdata.ObjectAt(state["input"])

	switch agentdata.StringField(part, "tool") {
	case "edit", "write":
		candidates := make([]langdetect.Candidate, 0)
		for _, change := range partChanges(part, cwd) {
			candidates = append(candidates, langdetect.Candidate{Path: change.Path, Weight: writeWeight})
		}
		return candidates
	case "read":
		path := usage.ResolvePath(cwd, agentdata.StringField(input, "filePath"))
		if path == "" {
			return nil
		}
		return []langdetect.Candidate{{Path: path, Weight: readWeight}}
	case "bash":
		// A shell command names the files it runs against; they are a weak but
		// real signal when a turn neither read nor wrote anything.
		return pathCandidates(agentdata.StringField(input, "command"), textWeight)
	case "glob", "grep":
		return pathCandidates(agentdata.StringField(input, "pattern"), textWeight)
	default:
		return nil
	}
}

func pathCandidates(text string, weight int) []langdetect.Candidate {
	paths := langdetect.PathsFromText(text)
	candidates := make([]langdetect.Candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, langdetect.Candidate{Path: path, Weight: weight})
	}
	return candidates
}

// partChanges extracts the file edits one message part performed. Only tool
// calls count: a "patch" part is a git snapshot of the whole worktree, which
// lists files nobody edited and carries no line counts, so treating it as
// agent work would invent writes.
//
// Kilo attaches the unified diff of every edit and write to the part
// (state.metadata.filediff.patch), so both tools are counted the same way:
// from the diff when it is there, from the tool's input when it is not.
func partChanges(part map[string]any, cwd string) []usage.FileChange {
	if agentdata.StringField(part, "type") != "tool" {
		return nil
	}
	state := agentdata.ObjectAt(part["state"])
	if agentdata.StringField(state, "status") != "completed" {
		return nil
	}
	input := agentdata.ObjectAt(state["input"])
	metadata := agentdata.ObjectAt(state["metadata"])

	tool := agentdata.StringField(part, "tool")
	if tool != "edit" && tool != "write" {
		return nil
	}

	diff := agentdata.ObjectAt(metadata["filediff"])
	path := usage.ResolvePath(cwd, agentdata.FirstNonEmpty(
		agentdata.StringField(diff, "file"),
		agentdata.StringField(input, "filePath"),
		agentdata.StringField(metadata, "filepath"),
	))
	if path == "" {
		return nil
	}

	if added, removed, ok := countUnifiedDiff(agentdata.StringField(diff, "patch")); ok {
		return []usage.FileChange{{Path: path, LinesAdded: added, LinesRemoved: removed}}
	}

	// Without the diff, the tool's input still bounds the change. A write's
	// whole content is added lines; whatever an overwrite displaced is not
	// recorded anywhere, so it stays uncounted rather than guessed at.
	if tool == "write" {
		return []usage.FileChange{{Path: path, LinesAdded: usage.CountLines(agentdata.StringField(input, "content"))}}
	}
	return []usage.FileChange{{
		Path:         path,
		LinesAdded:   usage.CountLines(agentdata.StringField(input, "newString")),
		LinesRemoved: usage.CountLines(agentdata.StringField(input, "oldString")),
	}}
}

// countUnifiedDiff counts the added and removed lines of a unified diff.
// The "+++"/"---" file headers are markup, not changes.
func countUnifiedDiff(patch string) (added, removed uint64, ok bool) {
	if strings.TrimSpace(patch) == "" {
		return 0, 0, false
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed, true
}

// stableMessageID keys an entry to the message id Kilo assigned, so
// re-ingesting the same message never double-counts.
func stableMessageID(messageID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	return usage.StableID("kilo", strings.TrimSpace(messageID))
}
