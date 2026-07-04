package usagescan

import (
	"errors"
	"fmt"
	"sort"

	"github.com/labx/tokitoki-agent/internal/claudeusage"
	"github.com/labx/tokitoki-agent/internal/codexusage"
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

type Scanner struct {
	db            *usagedb.DB
	providers     map[usage.Provider]usageprovider.Provider
	providerOrder []usage.Provider
}

// Result describes how many usage events were processed for each provider.
type Result struct {
	Providers map[usage.Provider]ProviderResult `json:"providers,omitempty"`
}

// ProviderResult describes the entries loaded and inserted for one provider.
type ProviderResult struct {
	EventsParsed   int `json:"events_parsed"`
	EventsInserted int `json:"events_inserted"`
}

// DefaultProviders returns the built-in usage providers.
func DefaultProviders() []usageprovider.Provider {
	return []usageprovider.Provider{
		claudeusage.Provider{},
		codexusage.Provider{},
	}
}

// New creates a scanner. When providers is empty, the built-in providers are
// used. Tests and new agent integrations can pass custom providers directly.
func New(db *usagedb.DB, providers ...usageprovider.Provider) *Scanner {
	if len(providers) == 0 {
		providers = DefaultProviders()
	}
	scanner := &Scanner{
		db:        db,
		providers: make(map[usage.Provider]usageprovider.Provider, len(providers)),
	}
	for _, provider := range providers {
		scanner.registerProvider(provider)
	}
	return scanner
}

// Scan loads entries from the selected provider data roots.
func (s *Scanner) Scan(providerDirs map[usage.Provider][]string) (Result, error) {
	var result Result
	var errs []error

	for _, providerID := range s.scanOrder(providerDirs) {
		dirs := filterPaths(providerDirs[providerID])
		if len(dirs) == 0 {
			continue
		}
		provider, ok := s.providers[providerID]
		if !ok {
			errs = append(errs, fmt.Errorf("no usage provider registered for %q", providerID))
			continue
		}
		providerResult, err := s.scanProvider(provider, dirs)
		result.setProviderResult(providerID, providerResult)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return result, errors.Join(errs...)
}

func (s *Scanner) scanProvider(provider usageprovider.Provider, paths []string) (ProviderResult, error) {
	var result ProviderResult
	entries, err := provider.Entries(paths)
	if err != nil {
		return result, err
	}
	inserted, err := s.db.InsertEvents(entries)
	if err != nil {
		return result, err
	}
	result.EventsParsed = len(entries)
	result.EventsInserted = inserted
	return result, nil
}

func (s *Scanner) registerProvider(provider usageprovider.Provider) {
	if provider == nil {
		return
	}
	providerID := provider.Provider()
	if _, exists := s.providers[providerID]; !exists {
		s.providerOrder = append(s.providerOrder, providerID)
	}
	s.providers[providerID] = provider
}

func (s *Scanner) scanOrder(providerDirs map[usage.Provider][]string) []usage.Provider {
	seen := make(map[usage.Provider]bool, len(providerDirs))
	order := make([]usage.Provider, 0, len(providerDirs))
	for _, providerID := range s.providerOrder {
		if len(filterPaths(providerDirs[providerID])) == 0 {
			continue
		}
		seen[providerID] = true
		order = append(order, providerID)
	}

	unknown := make([]usage.Provider, 0)
	for providerID, dirs := range providerDirs {
		if seen[providerID] || len(filterPaths(dirs)) == 0 {
			continue
		}
		unknown = append(unknown, providerID)
	}
	sort.Slice(unknown, func(i, j int) bool {
		return unknown[i] < unknown[j]
	})
	return append(order, unknown...)
}

func (r *Result) setProviderResult(provider usage.Provider, result ProviderResult) {
	if r.Providers == nil {
		r.Providers = make(map[usage.Provider]ProviderResult)
	}
	r.Providers[provider] = result
}

func filterPaths(paths []string) []string {
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			filtered = append(filtered, path)
		}
	}
	return filtered
}
