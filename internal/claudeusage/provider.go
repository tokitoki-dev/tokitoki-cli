package claudeusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider loads Claude usage entries.
type Provider struct {
	paths []string
}

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Claude provider configured with data roots.
func (Provider) WithPaths(paths []string) usageprovider.Provider {
	return Provider{paths: append([]string{}, paths...)}
}

// Provider returns the Claude provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderClaude
}

// Entries loads normalized Claude usage entries.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := LoadEntriesFromPaths(p.paths, "")
	if err != nil {
		return nil, err
	}
	return ConvertEntries(entries), nil
}
