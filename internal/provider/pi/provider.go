package pi

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads pi-agent usage entries.
type Provider struct{ shared.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a pi-agent provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = shared.NewBase(paths)
	return p
}

// Provider returns the pi-agent provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderPi }

// WithFileFilter returns a pi-agent provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized pi-agent usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return shared.SortEntriesByTimestampDesc(entries), nil
}
