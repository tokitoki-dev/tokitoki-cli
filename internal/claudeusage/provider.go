package claudeusage

import (
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

// Entries loads normalized Claude usage entries.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := LoadEntriesFromPaths(p.paths, "", p.filter)
	if err != nil {
		return nil, err
	}
	return ConvertEntries(entries), nil
}
