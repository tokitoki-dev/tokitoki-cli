package usagescan

import (
	"errors"
	"fmt"
	"os"
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

// Result describes how many files and events were processed for each provider.
type Result struct {
	Providers map[usage.Provider]usagedb.ScanResult `json:"providers,omitempty"`
	Claude    usagedb.ScanResult                    `json:"claude"`
	Codex     usagedb.ScanResult                    `json:"codex"`
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

// Scan reads usage files from the directories provided by the caller. An empty
// directory means that provider is skipped entirely: there is no default
// location and no fallback. The caller (native client) owns where the data is.
func (s *Scanner) Scan(claudeDir, codexDir string) (Result, error) {
	return s.ScanProviders(map[usage.Provider][]string{
		usage.ProviderClaude: nonEmptyPaths(claudeDir),
		usage.ProviderCodex:  nonEmptyPaths(codexDir),
	})
}

// ScanProviders reads usage files from the selected provider directories.
func (s *Scanner) ScanProviders(providerDirs map[usage.Provider][]string) (Result, error) {
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
		providerResult, err := s.scanProvider(provider, provider.UsageFiles(dirs))
		result.setProviderResult(providerID, providerResult)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return result, errors.Join(errs...)
}

func (s *Scanner) scanProvider(provider usageprovider.Provider, files []string) (usagedb.ScanResult, error) {
	var result usagedb.ScanResult
	var errs []error
	providerID := provider.Provider()
	for _, file := range files {
		result.FilesSeen++
		info, err := os.Stat(file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		existing, ok, err := s.db.SourceFile(providerID, file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok && existing.Size == info.Size() && existing.ModTimeUnixNS == info.ModTime().UnixNano() {
			result.FilesSkipped++
			continue
		}

		entries, err := provider.ReadUsageFile(file)
		source := usagedb.FileSource(providerID, file, info)
		if err != nil {
			source.LastError = err.Error()
			_ = s.db.SaveSourceFile(source)
			errs = append(errs, err)
			continue
		}
		inserted, err := s.db.InsertEvents(entries)
		if err != nil {
			source.LastError = err.Error()
			_ = s.db.SaveSourceFile(source)
			errs = append(errs, err)
			continue
		}
		if err := s.db.SaveSourceFile(source); err != nil {
			errs = append(errs, err)
			continue
		}
		result.FilesScanned++
		result.EventsParsed += len(entries)
		result.EventsInserted += inserted
	}
	return result, errors.Join(errs...)
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

func (r *Result) setProviderResult(provider usage.Provider, result usagedb.ScanResult) {
	if r.Providers == nil {
		r.Providers = make(map[usage.Provider]usagedb.ScanResult)
	}
	r.Providers[provider] = result

	switch provider {
	case usage.ProviderClaude:
		r.Claude = result
	case usage.ProviderCodex:
		r.Codex = result
	}
}

func nonEmptyPaths(path string) []string {
	if path == "" {
		return nil
	}
	return []string{path}
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
