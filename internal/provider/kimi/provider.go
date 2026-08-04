package kimi

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Kimi usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Kimi provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// Provider returns the Kimi provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderKimi }

// WithFileFilter returns a Kimi provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized Kimi usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}

// StreamEntries parses each wire file from where the previous scan stopped.
// Wire files are append-only JSONL and each line stands alone.
//
// Entries loads with a cross-file seen set because it returns one flat slice;
// here the same duplicates collapse on the database's primary key, so no
// in-memory state is carried between files.
func (p Provider) StreamEntries(resume func(path string) int64, emit func(path string, entries []usage.Entry, offset int64) error) error {
	return usageprovider.StreamFiles(wireFiles(p.Paths()), p.Filter(), parseWireFileFrom, emit, resume)
}
