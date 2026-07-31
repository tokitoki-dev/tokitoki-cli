package openclaw

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/shared"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads OpenClaw usage entries.
type Provider struct{ shared.Base }

var _ usageprovider.Provider = Provider{}

// WithPaths returns a OpenClaw provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.Base = shared.NewBase(paths)
	return p
}

// Provider returns the OpenClaw provider id.
func (Provider) Provider() usage.Provider { return usage.ProviderOpenClaw }

// WithFileFilter returns a OpenClaw provider that skips source files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.Base = p.WithFilterSet(filter)
	return p
}

// Entries loads normalized OpenClaw usage entries, newest first.
func (p Provider) Entries() ([]usage.Entry, error) {
	entries, err := loadEntries(p.Paths(), p.Filter())
	if err != nil {
		return nil, err
	}
	return shared.SortEntriesByTimestampDesc(entries), nil
}
