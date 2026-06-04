package usagescan

import (
	"errors"
	"os"

	"github.com/labx/tracklm-goagent/internal/claudeusage"
	"github.com/labx/tracklm-goagent/internal/codexusage"
	"github.com/labx/tracklm-goagent/internal/usage"
	"github.com/labx/tracklm-goagent/internal/usagedb"
)

type Scanner struct {
	db *usagedb.DB
}

type Result struct {
	Claude usagedb.ScanResult `json:"claude"`
	Codex  usagedb.ScanResult `json:"codex"`
}

func New(db *usagedb.DB) *Scanner {
	return &Scanner{db: db}
}

func (s *Scanner) ScanAll() (Result, error) {
	var result Result
	var errs []error

	claudePaths, err := claudeusage.ClaudePaths()
	if err == nil {
		result.Claude, err = s.scanProvider(usage.ProviderClaude, claudeusage.UsageFiles(claudePaths, ""), func(path string) ([]usage.Entry, error) {
			return claudeusage.UsageEntriesFromFile(path)
		})
	}
	if err != nil && !errors.Is(err, claudeusage.ErrNoDataDirs) {
		errs = append(errs, err)
	}

	codexPaths, err := codexusage.CodexPaths()
	if err == nil {
		result.Codex, err = s.scanProvider(usage.ProviderCodex, codexusage.UsageFiles(codexPaths), codexusage.ReadUsageFile)
	}
	if err != nil && !errors.Is(err, codexusage.ErrNoDataDirs) {
		errs = append(errs, err)
	}

	return result, errors.Join(errs...)
}

func (s *Scanner) scanProvider(provider usage.Provider, files []string, readFile func(string) ([]usage.Entry, error)) (usagedb.ScanResult, error) {
	var result usagedb.ScanResult
	var errs []error
	for _, file := range files {
		result.FilesSeen++
		info, err := os.Stat(file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		existing, ok, err := s.db.SourceFile(provider, file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok && existing.Size == info.Size() && existing.ModTimeUnixNS == info.ModTime().UnixNano() {
			result.FilesSkipped++
			continue
		}

		entries, err := readFile(file)
		source := usagedb.FileSource(provider, file, info)
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
