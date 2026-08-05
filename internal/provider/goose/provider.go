package goose

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Goose usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Goose provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// Provider returns the Goose provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderGoose }

// Entries loads normalized Goose usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}
