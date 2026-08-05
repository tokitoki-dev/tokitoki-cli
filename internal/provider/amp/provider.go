package amp

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Amp usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Amp provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// Provider returns the Amp provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderAmp }

// WithFileFilter returns a Amp provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized Amp usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}
