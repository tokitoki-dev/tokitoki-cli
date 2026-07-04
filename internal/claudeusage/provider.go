package claudeusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider loads Claude usage entries.
type Provider struct{}

var _ usageprovider.Provider = Provider{}

// Provider returns the Claude provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderClaude
}

// Entries loads normalized Claude usage entries below data roots.
func (Provider) Entries(paths []string) ([]usage.Entry, error) {
	entries, err := LoadEntriesFromPaths(paths, "")
	if err != nil {
		return nil, err
	}
	return ConvertEntries(entries), nil
}
