package codexusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider parses Codex usage files.
type Provider struct{}

var _ usageprovider.Provider = Provider{}

// Provider returns the Codex provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderCodex
}

// UsageFiles discovers Codex usage files below data directories.
func (Provider) UsageFiles(paths []string) []string {
	return UsageFiles(paths)
}

// ReadUsageFile parses a Codex usage file into normalized usage entries.
func (Provider) ReadUsageFile(path string) ([]usage.Entry, error) {
	return ReadUsageFile(path)
}
