package claude

import (
	"sort"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Claude usage entries.
type Provider struct {
	paths  []string
	filter usage.FileFilter
}

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Claude provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.paths = append([]string{}, paths...)
	return p
}

// WithFileFilter returns a Claude provider that skips session files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Provider returns the Claude provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderClaude
}

// Entries loads normalized Claude usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := LoadEntriesFromPaths(p.paths, "", p.filter)
	if err != nil {
		return nil, err
	}
	converted := ConvertEntries(entries)
	sort.Slice(converted, func(i, j int) bool {
		return converted[i].Timestamp.After(converted[j].Timestamp)
	})
	return converted, nil
}

// StreamEntries parses each transcript from where the previous scan stopped
// and hands its entries to emit before moving on.
//
// Transcripts are append-only, so resuming at a byte offset reads only what
// was written since the last scan. An active session's transcript grows to
// tens of megabytes, and re-reading all of it every few minutes to pick up
// the newest few lines is the cost this avoids.
//
// Entries are not sorted here. Ordering is a presentation concern of Entries;
// what emit does with these is store them, keyed by id.
func (p Provider) StreamEntries(resume func(path string) int64, emit func(path string, entries []usage.Entry, offset int64) error) error {
	for _, file := range UsageFiles(p.paths, "") {
		if p.filter != nil && !p.filter(file) {
			continue
		}
		loaded, offset, err := ReadUsageFileFrom(file, resume(file))
		if err != nil {
			return err
		}
		if err := emit(file, ConvertEntries(loaded), offset); err != nil {
			return err
		}
	}
	return nil
}
