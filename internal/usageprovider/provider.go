package usageprovider

import "github.com/labx/tokitoki-agent/internal/usage"

// Provider parses usage files for one local AI agent.
type Provider interface {
	// Provider returns the stable provider id written to usage events and
	// source-file scan state.
	Provider() usage.Provider

	// UsageFiles discovers usage files below the provided data directories.
	UsageFiles(paths []string) []string

	// ReadUsageFile parses one usage file into normalized usage entries.
	ReadUsageFile(path string) ([]usage.Entry, error)
}
