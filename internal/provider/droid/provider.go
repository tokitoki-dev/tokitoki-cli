package droid

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Droid usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Droid provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// ReportsRunningTotals says each entry is a session's total so far — Droid
// rewrites a session's settings file as the session grows — not one API call; the store keeps the
// growth between reads (usagedb.InsertGrowth).
func (Provider) ReportsRunningTotals() bool { return true }

// Provider returns the Droid provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderDroid }

// WithFileFilter returns a Droid provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized Droid usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}
