package usagescan

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/projectfile"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/amp"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/claude"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/codebuff"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/codex"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/copilot"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/droid"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/gemini"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/goose"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/hermes"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/kilo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/kimi"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/openclaw"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/opencode"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/pi"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/qwen"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

type Scanner struct {
	db            *usagedb.DB
	providers     map[usage.Provider]usageprovider.Provider
	providerOrder []usage.Provider

	// Logger receives warnings about project identity files that exist but
	// cannot be read. Nil discards them.
	Logger *slog.Logger
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
		claude.Provider{},
		codex.Provider{},
		copilot.Provider{},
		gemini.Provider{},
		kimi.Provider{},
		qwen.Provider{},
		openclaw.Provider{},
		pi.Provider{},
		amp.Provider{},
		droid.Provider{},
		kilo.Provider{},
		hermes.Provider{},
		codebuff.Provider{},
		opencode.Provider{},
		goose.Provider{},
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

	// Stat snapshots of files already ingested. On error scan everything:
	// re-parsing is wasted work, skipping is lost data.
	scanned, err := s.db.ScannedFiles()
	if err != nil {
		scanned = nil
		if s.Logger != nil {
			s.Logger.Warn("scanned file states unavailable, scanning all files", "error", err)
		}
	}

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
		providerResult, err := s.scanProvider(provider, dirs, scanned)
		result.setProviderResult(providerID, providerResult)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return result, errors.Join(errs...)
}

func (s *Scanner) scanProvider(provider usageprovider.Provider, paths []string, scanned map[string]usagedb.FileState) (ProviderResult, error) {
	var result ProviderResult
	configured := providerWithPaths(provider, paths)
	pending := make(map[string]usagedb.FileState)
	if filterable, ok := configured.(filterConfiguredProvider); ok && scanned != nil {
		configured = filterable.WithFileFilter(func(path string) bool {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				return true
			}
			state := usagedb.FileState{Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}
			if previous, ok := scanned[path]; ok && previous == state {
				return false
			}
			pending[path] = state
			return true
		})
	}
	entries, err := configured.Entries()
	if err != nil {
		return result, err
	}
	s.applyProjectFiles(entries)
	inserted, err := s.db.InsertEvents(entries)
	if err != nil {
		return result, err
	}
	// The stat snapshots were taken before parsing, so a write that lands
	// mid-scan still changes the stored state and forces a re-scan.
	if err := s.db.UpsertScannedFiles(pending); err != nil {
		return result, err
	}
	result.EventsParsed = len(entries)
	result.EventsInserted = inserted
	return result, nil
}

// applyProjectFiles rewrites each entry's identity from the nearest project
// identity file. An identity file is an optional override: one that exists
// but cannot be read is warned about and skipped — a stray unreadable
// .tokitoki file somewhere on disk must never stop usage from flowing.
func (s *Scanner) applyProjectFiles(entries []usage.Entry) {
	type cacheKey struct {
		entityDir   string
		projectPath string
		branch      string
	}
	type cacheValue struct {
		result projectfile.Result
		found  bool
	}
	cache := make(map[cacheKey]cacheValue)

	for i := range entries {
		input := projectfile.Input{
			Entity:      entries[i].Entity,
			ProjectPath: entries[i].ProjectPath,
			Branch:      entries[i].Branch,
		}
		key := cacheKey{
			entityDir:   projectSearchDirectory(input.Entity, true),
			projectPath: projectSearchDirectory(input.ProjectPath, false),
			branch:      strings.TrimSpace(input.Branch),
		}
		cached, ok := cache[key]
		if !ok {
			resolved, found, err := projectfile.Resolve(input)
			if err != nil {
				if s.Logger != nil {
					s.Logger.Warn("project identity file ignored", "error", err)
				}
				// Cache the miss too: the same broken file would fail
				// identically for every sibling event.
				found = false
			}
			cached = cacheValue{result: resolved, found: found}
			cache[key] = cached
		}
		if !cached.found {
			continue
		}
		entries[i].Project = cached.result.Project
		entries[i].ProjectPath = cached.result.ProjectPath
		entries[i].Branch = cached.result.Branch
	}
}

func projectSearchDirectory(path string, isFile bool) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	if isFile {
		return filepath.Dir(path)
	}
	return path
}

type pathConfiguredProvider interface {
	WithPaths(paths []string) usageprovider.Provider
}

// filterConfiguredProvider is implemented by providers that can skip source
// files the filter rejects. Providers without it are always fully scanned.
type filterConfiguredProvider interface {
	WithFileFilter(filter usage.FileFilter) usageprovider.Provider
}

func providerWithPaths(provider usageprovider.Provider, paths []string) usageprovider.Provider {
	configured, ok := provider.(pathConfiguredProvider)
	if !ok {
		return provider
	}
	return configured.WithPaths(paths)
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
