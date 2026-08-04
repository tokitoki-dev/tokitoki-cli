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

// StreamFiles walks files in order, parsing each from where the previous scan
// stopped and handing its entries to emit before moving to the next.
//
// It is the shared body of every append-only provider's StreamEntries: the
// providers differ only in how they find their files and how they parse one,
// so those are the two arguments. Keeping the loop here means the rules that
// make a resumed scan safe — parse from the recorded offset, emit before
// advancing it — are stated once rather than re-derived per provider.
func StreamFiles(
	files []string,
	filter usage.FileFilter,
	parse func(path string, start int64) ([]usage.Entry, int64, error),
	emit func(path string, entries []usage.Entry, offset int64) error,
	resume func(path string) int64,
) error {
	for _, file := range files {
		if filter != nil && !filter(file) {
			continue
		}
		entries, offset, err := parse(file, resume(file))
		if err != nil {
			return err
		}
		if err := emit(file, entries, offset); err != nil {
			return err
		}
	}
	return nil
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

// StableEntryID derives a deterministic id from an entry's source position
// and contents.
//
// The position is the byte offset, not the line number. A scan that resumes
// mid-file starts counting lines from 1 again, so a line-based id would give
// the same record a different identity depending on where the previous pass
// happened to stop. The byte offset is a property of the file itself and does
// not move.
func StableEntryID(entry usage.Entry, extra ...string) string {
	parts := []string{
		string(entry.Provider),
		entry.SourceFile,
		strconv.FormatInt(entry.SourceStart, 10),
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
