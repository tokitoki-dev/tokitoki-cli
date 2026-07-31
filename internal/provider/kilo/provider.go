package kilo

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Kilo usage entries.
type Provider struct{ shared.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Kilo provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = shared.NewBase(paths)
	return p
}

// Provider returns the Kilo provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderKilo }

// Entries loads normalized Kilo usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths())
	if err != nil {
		return nil, err
	}
	return shared.SortEntriesByTimestampDesc(entries), nil
}
