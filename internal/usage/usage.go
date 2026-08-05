package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Provider string

const (
	ProviderClaude    Provider = "claude"
	ProviderCodex     Provider = "codex"
	ProviderCopilot   Provider = "copilot"
	ProviderGemini    Provider = "gemini"
	ProviderKimi      Provider = "kimi"
	ProviderQwen      Provider = "qwen"
	ProviderOpenClaw  Provider = "openclaw"
	ProviderPi        Provider = "pi"
	ProviderAmp       Provider = "amp"
	ProviderDroid     Provider = "droid"
	ProviderKilo      Provider = "kilo"
	ProviderHermes    Provider = "hermes"
	ProviderCodebuff  Provider = "codebuff"
	ProviderOpenCode  Provider = "opencode"
	ProviderGoose     Provider = "goose"
	ProviderWorkbuddy Provider = "workbuddy"
)

const UnknownLanguage = "Unknown"

// UnknownProject is the single spelling every provider uses when a project
// name cannot be determined.
const UnknownProject = "Unknown"

// NormalizeProject maps empty and legacy "unknown" spellings to
// UnknownProject so undetermined projects look the same everywhere.
func NormalizeProject(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "unknown") {
		return UnknownProject
	}
	return name
}

// FileFilter reports whether a source file must be parsed. Returning false
// means the file's events are already ingested and parsing it again would be
// wasted work. A nil FileFilter parses everything.
type FileFilter func(path string) bool

type TokenUsage struct {
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens,omitempty"`
	CachedInputTokens        uint64 `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64 `json:"total_tokens"`
}

// FileChange records the diff one event applied to a single file.
type FileChange struct {
	Path         string `json:"path"`
	LinesAdded   uint64 `json:"lines_added,omitempty"`
	LinesRemoved uint64 `json:"lines_removed,omitempty"`
}

// ProjectFromCWD derives the project path and name from a working directory an
// agent recorded. It reports false for values that cannot name a project
// (empty, relative, or the filesystem root), so callers keep their fallback.
func ProjectFromCWD(cwd string) (string, string, bool) {
	clean := strings.TrimSpace(cwd)
	if clean == "" || !filepath.IsAbs(clean) {
		return "", "", false
	}
	clean = filepath.Clean(clean)
	name := filepath.Base(clean)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		return "", "", false
	}
	return clean, name, true
}

// CountLines counts the source lines in a blob of file content. A trailing
// newline does not add a line, so "a\nb\n" and "a\nb" both count as 2.
func CountLines(content string) uint64 {
	if content == "" {
		return 0
	}
	lines := uint64(strings.Count(content, "\n"))
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines
}

// ResolvePath makes a file path recorded by an agent absolute, interpreting a
// relative one against the working directory the agent ran in.
func ResolvePath(cwd, path string) string {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return ""
	case filepath.IsAbs(path):
		return filepath.Clean(path)
	case strings.TrimSpace(cwd) == "":
		return path
	default:
		return filepath.Join(cwd, path)
	}
}

// ApplyFileChange folds one file's diff into an entry: it accumulates the
// per-file totals, keeps the entry totals in sync, and re-points Entity at the
// most-changed file. Every provider that records diffs funnels through here so
// "Entity is the biggest change" holds identically everywhere.
func (e *Entry) ApplyFileChange(change FileChange) {
	e.LinesAdded += change.LinesAdded
	e.LinesRemoved += change.LinesRemoved
	write := true
	e.IsWrite = &write
	if change.Path == "" {
		return
	}

	found := false
	for i := range e.Files {
		if e.Files[i].Path == change.Path {
			e.Files[i].LinesAdded += change.LinesAdded
			e.Files[i].LinesRemoved += change.LinesRemoved
			found = true
			break
		}
	}
	if !found {
		e.Files = append(e.Files, change)
	}

	best, bestWeight := "", uint64(0)
	for _, file := range e.Files {
		if weight := file.LinesAdded + file.LinesRemoved; weight >= bestWeight {
			best, bestWeight = file.Path, weight
		}
	}
	e.Entity = best
	e.EntityType = "file"
}

type Entry struct {
	Provider    Provider  `json:"provider"`
	ID          string    `json:"id,omitempty"`
	SourceType  string    `json:"source_type,omitempty"`
	EventKind   string    `json:"event_kind,omitempty"`
	SourceFile  string    `json:"source_file,omitempty"`
	SourceLine  int       `json:"source_line,omitempty"`
	SourceStart int64     `json:"source_start,omitempty"`
	SourceEnd   int64     `json:"source_end,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Date        string    `json:"date"`
	Project     string    `json:"project"`
	ProjectPath string    `json:"project_path,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Model       string    `json:"model,omitempty"`
	Language    string    `json:"language"`
	// OS is the operating system of the machine that produced this entry,
	// e.g. "macOS", "Windows", "Linux".
	OS string `json:"os,omitempty"`
	// Client is the human-readable IDE or app source the request came from.
	// VS Code plugins are normalized across providers, but standalone apps
	// remain product-specific, e.g. "VS Code", "Codex Desktop", "Claude CLI".
	Client     string `json:"client,omitempty"`
	Entity     string `json:"entity,omitempty"`
	EntityType string `json:"entity_type,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Editor     string `json:"editor,omitempty"`
	Category   string `json:"category,omitempty"`
	IsWrite    *bool  `json:"is_write,omitempty"`
	// LinesAdded/LinesRemoved count the source lines the agent added and
	// removed in this event's file modifications, for providers that record
	// diffs. Zero means "no diff recorded", not "no change".
	LinesAdded   uint64 `json:"lines_added,omitempty"`
	LinesRemoved uint64 `json:"lines_removed,omitempty"`
	// Files breaks the same modifications down per file. Entity is always
	// the most-changed path in here; LinesAdded/LinesRemoved are the totals.
	Files []FileChange   `json:"files,omitempty"`
	Raw   map[string]any `json:"raw,omitempty"`
	Usage TokenUsage     `json:"usage"`
}

// NormalizeOS maps a Go runtime.GOOS value to a human-readable name.
func NormalizeOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return goos
	}
}

// NormalizeClient trims a provider-specific source token (Claude's
// "entrypoint" or Codex's "originator") and reports it verbatim.
//
// The token is deliberately not mapped to a display name here. Every fork of
// an editor reports its own token, so a client-side table can only ever name
// the forks that existed when the binary shipped — anything newer would be
// silently mislabelled as the product it forked from. Reporting the raw token
// keeps that information intact and leaves naming to the server, which can
// learn a new source without waiting for clients to update.
func NormalizeClient(raw string) string {
	return strings.TrimSpace(raw)
}

func NormalizeLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return UnknownLanguage
	}
	return language
}

func StableID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

type DailyProjectSummary struct {
	Provider                 Provider `json:"provider"`
	Date                     string   `json:"date"`
	Project                  string   `json:"project"`
	ProjectPath              string   `json:"project_path,omitempty"`
	InputTokens              uint64   `json:"input_tokens"`
	OutputTokens             uint64   `json:"output_tokens"`
	CacheCreationInputTokens uint64   `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64   `json:"cache_read_input_tokens,omitempty"`
	CachedInputTokens        uint64   `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64   `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64   `json:"total_tokens"`
}

func SummarizeDailyProjects(entries []Entry) []DailyProjectSummary {
	type key struct {
		provider Provider
		date     string
		project  string
		path     string
	}
	indexes := map[key]int{}
	summaries := make([]DailyProjectSummary, 0)
	for _, entry := range entries {
		key := key{
			provider: entry.Provider,
			date:     entry.Date,
			project:  entry.Project,
			path:     entry.ProjectPath,
		}
		index, ok := indexes[key]
		if !ok {
			index = len(summaries)
			indexes[key] = index
			summaries = append(summaries, DailyProjectSummary{
				Provider:    entry.Provider,
				Date:        entry.Date,
				Project:     entry.Project,
				ProjectPath: entry.ProjectPath,
			})
		}
		summary := &summaries[index]
		summary.InputTokens += entry.Usage.InputTokens
		summary.OutputTokens += entry.Usage.OutputTokens
		summary.CacheCreationInputTokens += entry.Usage.CacheCreationInputTokens
		summary.CacheReadInputTokens += entry.Usage.CacheReadInputTokens
		summary.CachedInputTokens += entry.Usage.CachedInputTokens
		summary.ReasoningOutputTokens += entry.Usage.ReasoningOutputTokens
		summary.TotalTokens += entry.Usage.TotalTokens
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Provider != summaries[j].Provider {
			return summaries[i].Provider < summaries[j].Provider
		}
		if summaries[i].Project != summaries[j].Project {
			return summaries[i].Project < summaries[j].Project
		}
		return summaries[i].Date < summaries[j].Date
	})
	return summaries
}
