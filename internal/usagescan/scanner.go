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
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/workbuddy"
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
		workbuddy.Provider{},
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
	if streamer, ok := configured.(streamProvider); ok {
		return s.scanStreaming(streamer, scanned)
	}
	pending := make(map[string]usagedb.FileState)
	if filterable, ok := configured.(filterConfiguredProvider); ok && scanned != nil {
		configured = filterable.WithFileFilter(func(path string) bool {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				return true
			}
			state := usagedb.FileState{Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}
			if previous, ok := scanned[path]; ok && unchanged(previous, state) {
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

// unchanged reports whether a file's current stat matches the one recorded
// when it was last scanned, meaning it holds nothing new.
//
// Only size and mtime are compared. Offset is where the last pass stopped
// reading, not a property of the file, and a file that ended mid-line has an
// offset behind its size while still being unchanged.
func unchanged(previous, current usagedb.FileState) bool {
	return previous.Size == current.Size && previous.MtimeNS == current.MtimeNS
}

// fullyConsumed reports whether the recorded offset reached the end of the
// recorded size.
//
// A file can be unchanged and still hold unread bytes: a trailing partial line
// leaves the offset short, and so would any state written by a version that
// stat'd a file after parsing it. Treating "unchanged" alone as "nothing to
// do" would skip those bytes for as long as the file stays quiet — which for a
// finished session transcript is forever.
func fullyConsumed(state usagedb.FileState) bool {
	return state.Offset >= state.Size
}

// scanStreaming ingests a provider one source file at a time, committing each
// file's events and then recording where parsing stopped.
//
// The order within a file is what makes an interrupted scan safe: events are
// stored first, and only then does the file's offset advance. A crash between
// the two costs a re-parse of one file, which is wasted work. The reverse
// order would record progress over events that were never stored, and that is
// lost data — see UpsertScannedFiles.
func (s *Scanner) scanStreaming(streamer streamProvider, scanned map[string]usagedb.FileState) (ProviderResult, error) {
	var result ProviderResult

	// A file whose stat is unchanged holds nothing new. Skipping it here is
	// what keeps a steady-state scan proportional to what was just written
	// rather than to the whole history on disk.
	if filterable, ok := streamer.(filterConfiguredProvider); ok && scanned != nil {
		if restreamed, ok := filterable.WithFileFilter(func(path string) bool {
			state, seen := scanned[path]
			if !seen {
				return true
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				return true
			}
			if !unchanged(state, usagedb.FileState{Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}) {
				return true
			}
			// Unchanged, but the last pass stopped short of the end. Those
			// bytes are still unread and this is the only thing that will
			// come back for them.
			return !fullyConsumed(state)
		}).(streamProvider); ok {
			streamer = restreamed
		}
	}

	// Stats taken when each file was handed to the parser. Recording the size
	// as it was *before* parsing is what makes an append during the parse
	// safe: stat'ing afterwards would pair a size that already counts the new
	// bytes with an offset that stops short of them, so the next scan would
	// see an unchanged size, skip the file, and lose everything written while
	// it was being read.
	seenAt := make(map[string]usagedb.FileState)

	resume := func(path string) int64 {
		info, err := os.Stat(path)
		if err == nil {
			seenAt[path] = usagedb.FileState{Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}
		}
		state, ok := scanned[path]
		if !ok || err != nil {
			return 0
		}
		// A file shorter than where we stopped was truncated or replaced, so
		// the stored offset points into content that no longer exists. The
		// only safe reading is from the beginning.
		if info.Size() < state.Offset {
			return 0
		}
		return state.Offset
	}

	emit := func(path string, entries []usage.Entry, offset int64) error {
		s.applyProjectFiles(entries)
		inserted, err := s.db.InsertEvents(entries)
		if err != nil {
			return err
		}
		result.EventsParsed += len(entries)
		result.EventsInserted += inserted

		state, ok := seenAt[path]
		if !ok {
			// Without a stat there is no honest state to record. Leaving the
			// previous one alone re-parses this file next time, which is the
			// harmless outcome.
			return nil
		}
		state.Offset = offset
		return s.db.UpsertScannedFiles(map[string]usagedb.FileState{path: state})
	}

	return result, streamer.StreamEntries(resume, emit)
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

// streamProvider is implemented by providers whose sources are append-only
// files that can be parsed one at a time and resumed mid-file. The scanner
// commits each file's events as they arrive instead of holding an entire
// provider's history in memory and writing it once at the end.
//
// It is deliberately optional. A provider whose events come from a SQLite
// database, or one that must join across sources before it knows anything,
// has no per-file boundary to commit on and keeps using Entries().
type streamProvider interface {
	// StreamEntries parses each source file and calls emit once per file with
	// that file's entries and the offset to resume from next time. Providers
	// pass the resume offset from resume(path) to their reader.
	//
	// emit returning an error aborts the scan: it means the events could not
	// be stored, and continuing would advance past data that was never saved.
	StreamEntries(resume func(path string) int64, emit func(path string, entries []usage.Entry, offset int64) error) error
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
