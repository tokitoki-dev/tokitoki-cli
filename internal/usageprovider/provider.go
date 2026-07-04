package usageprovider

import "github.com/labx/tokitoki-agent/internal/usage"

// Provider loads normalized usage entries for one local AI agent.
type Provider interface {
	// Provider returns the stable provider id written to usage events and
	// provider scan results.
	Provider() usage.Provider

	// Entries loads normalized usage entries from the provider's own source.
	Entries() ([]usage.Entry, error)
}
