package opencode

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads OpenCode usage entries.
type Provider struct{ shared.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a OpenCode provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = shared.NewBase(paths)
	return p
}

// Provider returns the OpenCode provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderOpenCode }

// WithFileFilter returns a OpenCode provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized OpenCode usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return shared.SortEntriesByTimestampDesc(entries), nil
}
