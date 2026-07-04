package codexusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider loads Codex usage entries.
type Provider struct{}

var _ usageprovider.Provider = Provider{}

// Provider returns the Codex provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderCodex
}

// Entries loads normalized Codex usage entries below data roots.
func (Provider) Entries(paths []string) ([]usage.Entry, error) {
	return LoadEntriesFromPaths(paths, "")
}
