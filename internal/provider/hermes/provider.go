package hermes

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Hermes Agent usage entries.
type Provider struct{ usageprovider.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Hermes Agent provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = usageprovider.NewBase(paths)
	return p
}

// ReportsRunningTotals says each entry is a session's total so far — Hermes
// rewrites a session's row in its sessions table as the session grows — not one API call; the store keeps the
// growth between reads (usagedb.InsertGrowth).
func (Provider) ReportsRunningTotals() bool { return true }

// Provider returns the Hermes Agent provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderHermes }

// Entries loads normalized Hermes Agent usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths())
	if err != nil {
		return nil, err
	}
	return usageprovider.SortEntriesByTimestampDesc(entries), nil
}
