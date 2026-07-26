package codexusage

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// Provider loads Codex usage entries.
type Provider struct {
	paths  []string
	filter usage.FileFilter
}

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Codex provider configured with data roots.
func (p Provider) WithPaths(paths []string) usageprovider.Provider {
	p.paths = append([]string{}, paths...)
	return p
}

// WithFileFilter returns a Codex provider that skips session files the
// filter rejects.
func (p Provider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Provider returns the Codex provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderCodex
}

// Entries loads normalized Codex usage entries.
func (p Provider) Entries() ([]usage.Entry, error) {
	return LoadEntriesFromPaths(p.paths, "", p.filter)
}
