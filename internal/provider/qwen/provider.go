package qwen

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Qwen usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Qwen provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// Provider returns the Qwen provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderQwen }

// WithFileFilter returns a Qwen provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized Qwen usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}

// StreamEntries parses each chat file from where the previous scan stopped.
// Chat files are append-only JSONL and each line stands alone, so a resumed
// read produces exactly what a whole read would.
func (p Provider) StreamEntries(resume func(path string) int64, emit func(path string, entries []usage.Entry, offset int64) error) error {
	return usageprovider.StreamFiles(chatFiles(p.Paths()), p.Filter(), parseChatFileFrom, emit, resume)
}
