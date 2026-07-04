package claudeusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// Provider parses Claude usage files.
type Provider struct{}

var _ usageprovider.Provider = Provider{}

// Provider returns the Claude provider id.
func (Provider) Provider() usage.Provider {
	return usage.ProviderClaude
}

// UsageFiles discovers Claude usage files below data directories.
func (Provider) UsageFiles(paths []string) []string {
	return UsageFiles(paths, "")
}

// ReadUsageFile parses a Claude usage file into normalized usage entries.
func (Provider) ReadUsageFile(path string) ([]usage.Entry, error) {
	return UsageEntriesFromFile(path)
}
