package usageprovider

import (
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Provider loads normalized usage entries for one local AI agent.
type Provider interface {
	// Provider returns the stable provider id written to usage events and
	// provider scan results.
	Provider() usage.Provider

	// Entries loads normalized usage entries from the provider's own source.
	Entries() ([]usage.Entry, error)
}

// Base carries the scan configuration every provider needs: where to look and
// which files are already ingested. Providers embed it so the accessors and
// the WithPaths/WithFileFilter plumbing exist in one place instead of once per
// provider.
type Base struct {
	paths  []string
	filter usage.FileFilter
}

// NewBase returns a Base scanning the given data roots.
func NewBase(paths []string) Base {
	return Base{paths: append([]string{}, paths...)}
}

// Paths returns the data roots to scan.
func (b Base) Paths() []string { return b.paths }

// Filter returns the file filter, or nil when every file must be parsed.
func (b Base) Filter() usage.FileFilter { return b.filter }

// WithPathsSet returns a copy scanning the given data roots.
func (b Base) WithPathsSet(paths []string) Base {
	b.paths = append([]string{}, paths...)
	return b
}

// WithFilterSet returns a copy that skips the source files filter rejects.
func (b Base) WithFilterSet(filter usage.FileFilter) Base {
	b.filter = filter
	return b
}

// SortEntriesByTimestampDesc orders entries newest first, the order every
// provider returns them in.
func SortEntriesByTimestampDesc(entries []usage.Entry) []usage.Entry {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	return entries
}

func formatDate(timestamp time.Time) string {
	return timestamp.In(time.Local).Format("2006-01-02")
}

func TotalUsage(tokens usage.TokenUsage) uint64 {
	return tokens.InputTokens +
		tokens.OutputTokens +
		tokens.CacheCreationInputTokens +
		tokens.CacheReadInputTokens +
		tokens.CachedInputTokens +
		tokens.ReasoningOutputTokens
}

func ApplyTotalFallback(tokens usage.TokenUsage, total uint64) usage.TokenUsage {
	sum := TotalUsage(tokens)
	if sum == 0 && total > 0 {
		tokens.OutputTokens = total
		tokens.TotalTokens = total
		return tokens
	}
	if total > sum {
		tokens.ReasoningOutputTokens += total - sum
		tokens.TotalTokens = total
		return tokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = sum
	}
	return tokens
}

func NonZero(tokens usage.TokenUsage) bool {
	return TotalUsage(tokens) > 0 || tokens.TotalTokens > 0
}

func BaseEntry(provider usage.Provider, timestamp time.Time, project, projectPath, sessionID, model, client string, tokens usage.TokenUsage) usage.Entry {
	return usage.Entry{
		Provider:    provider,
		Timestamp:   timestamp,
		Date:        formatDate(timestamp),
		Project:     project,
		ProjectPath: projectPath,
		SessionID:   sessionID,
		Model:       model,
		Language:    usage.UnknownLanguage,
		OS:          usage.NormalizeOS(runtime.GOOS),
		Client:      client,
		Usage:       tokens,
	}
}

func SetSource(entry *usage.Entry, source string, line int, start, end int64) {
	entry.SourceFile = source
	entry.SourceLine = line
	entry.SourceStart = start
	entry.SourceEnd = end
}

func StableEntryID(entry usage.Entry, extra ...string) string {
	parts := []string{
		string(entry.Provider),
		entry.SourceFile,
		strconv.Itoa(entry.SourceLine),
		entry.Timestamp.Format(time.RFC3339Nano),
		entry.Project,
		entry.ProjectPath,
		entry.SessionID,
		entry.Model,
		strconv.FormatUint(entry.Usage.InputTokens, 10),
		strconv.FormatUint(entry.Usage.OutputTokens, 10),
		strconv.FormatUint(entry.Usage.CacheCreationInputTokens, 10),
		strconv.FormatUint(entry.Usage.CacheReadInputTokens, 10),
		strconv.FormatUint(entry.Usage.CachedInputTokens, 10),
		strconv.FormatUint(entry.Usage.ReasoningOutputTokens, 10),
		strconv.FormatUint(entry.Usage.TotalTokens, 10),
	}
	parts = append(parts, extra...)
	return usage.StableID(parts...)
}

func SortEntries(entries []usage.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].Timestamp.Before(entries[j].Timestamp)
		}
		return entries[i].ID < entries[j].ID
	})
}
