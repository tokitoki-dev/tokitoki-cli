package codexusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider loads Codex usage entries.
type Provider struct {
	paths []string
}

var _ usageprovider.Provider = Provider{}

// WithPaths returns a Codex provider configured with data roots.
func (Provider) WithPaths(paths []string) usageprovider.Provider {
	return Provider{paths: append([]string{}, paths...)}
}

// Provider returns the Codex provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderCodex
}

// Entries loads normalized Codex usage entries.
func (p Provider) Entries() ([]usage.Entry, error) {
	return LoadEntriesFromPaths(p.paths, "")
}
