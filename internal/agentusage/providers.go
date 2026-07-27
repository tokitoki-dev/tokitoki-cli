package agentusage

import (
	"sort"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

func sortEntriesByTimestampDesc(entries []usage.Entry) []usage.Entry {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	return entries
}

// providerBase carries the scan configuration shared by every agent
// provider. filter skips source files whose events are already ingested;
// the SQLite-backed providers (Kilo, Hermes, Goose) deliberately do not
// implement WithFileFilter because WAL keeps the main database file's stat
// unchanged while data grows in the -wal journal.
type providerBase struct {
	paths  []string
	filter usage.FileFilter
}

func newProviderBase(paths []string) providerBase {
	return providerBase{paths: append([]string{}, paths...)}
}

// CopilotProvider loads GitHub Copilot CLI usage entries.
type CopilotProvider struct{ providerBase }

// GeminiProvider loads Gemini CLI usage entries.
type GeminiProvider struct{ providerBase }

// KimiProvider loads Kimi usage entries.
type KimiProvider struct{ providerBase }

// QwenProvider loads Qwen usage entries.
type QwenProvider struct{ providerBase }

// OpenClawProvider loads OpenClaw usage entries.
type OpenClawProvider struct{ providerBase }

// PiProvider loads pi-agent usage entries.
type PiProvider struct{ providerBase }

// AmpProvider loads Amp usage entries.
type AmpProvider struct{ providerBase }

// DroidProvider loads Droid usage entries.
type DroidProvider struct{ providerBase }

// KiloProvider loads Kilo usage entries.
type KiloProvider struct{ providerBase }

// HermesProvider loads Hermes Agent usage entries.
type HermesProvider struct{ providerBase }

// CodebuffProvider loads Codebuff usage entries.
type CodebuffProvider struct{ providerBase }

// OpenCodeProvider loads OpenCode usage entries.
type OpenCodeProvider struct{ providerBase }

// GooseProvider loads Goose usage entries.
type GooseProvider struct{ providerBase }

var (
	_ usageprovider.Provider = CopilotProvider{}
	_ usageprovider.Provider = GeminiProvider{}
	_ usageprovider.Provider = KimiProvider{}
	_ usageprovider.Provider = QwenProvider{}
	_ usageprovider.Provider = OpenClawProvider{}
	_ usageprovider.Provider = PiProvider{}
	_ usageprovider.Provider = AmpProvider{}
	_ usageprovider.Provider = DroidProvider{}
	_ usageprovider.Provider = KiloProvider{}
	_ usageprovider.Provider = HermesProvider{}
	_ usageprovider.Provider = CodebuffProvider{}
	_ usageprovider.Provider = OpenCodeProvider{}
	_ usageprovider.Provider = GooseProvider{}
)

// WithPaths returns a GitHub Copilot CLI provider configured with data roots.
func (CopilotProvider) WithPaths(paths []string) usageprovider.Provider {
	return CopilotProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the GitHub Copilot CLI provider id.
func (CopilotProvider) Provider() usage.Provider { return usage.ProviderCopilot }

// WithFileFilter returns a GitHub Copilot CLI provider that skips source
// files the filter rejects.
func (p CopilotProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized GitHub Copilot CLI usage entries, newest first.
func (p CopilotProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadCopilotEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Gemini CLI provider configured with data roots.
func (GeminiProvider) WithPaths(paths []string) usageprovider.Provider {
	return GeminiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Gemini CLI provider id.
func (GeminiProvider) Provider() usage.Provider { return usage.ProviderGemini }

// WithFileFilter returns a Gemini CLI provider that skips source files the
// filter rejects.
func (p GeminiProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Gemini CLI usage entries, newest first.
func (p GeminiProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadGeminiEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Kimi provider configured with data roots.
func (KimiProvider) WithPaths(paths []string) usageprovider.Provider {
	return KimiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Kimi provider id.
func (KimiProvider) Provider() usage.Provider { return usage.ProviderKimi }

// WithFileFilter returns a Kimi provider that skips source files the filter
// rejects.
func (p KimiProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Kimi usage entries, newest first.
func (p KimiProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadKimiEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Qwen provider configured with data roots.
func (QwenProvider) WithPaths(paths []string) usageprovider.Provider {
	return QwenProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Qwen provider id.
func (QwenProvider) Provider() usage.Provider { return usage.ProviderQwen }

// WithFileFilter returns a Qwen provider that skips source files the filter
// rejects.
func (p QwenProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Qwen usage entries, newest first.
func (p QwenProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadQwenEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns an OpenClaw provider configured with data roots.
func (OpenClawProvider) WithPaths(paths []string) usageprovider.Provider {
	return OpenClawProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the OpenClaw provider id.
func (OpenClawProvider) Provider() usage.Provider { return usage.ProviderOpenClaw }

// WithFileFilter returns an OpenClaw provider that skips source files the
// filter rejects.
func (p OpenClawProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized OpenClaw usage entries, newest first.
func (p OpenClawProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadOpenClawEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a pi-agent provider configured with data roots.
func (PiProvider) WithPaths(paths []string) usageprovider.Provider {
	return PiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the pi-agent provider id.
func (PiProvider) Provider() usage.Provider { return usage.ProviderPi }

// WithFileFilter returns a pi-agent provider that skips source files the
// filter rejects.
func (p PiProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized pi-agent usage entries, newest first.
func (p PiProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadPiEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns an Amp provider configured with data roots.
func (AmpProvider) WithPaths(paths []string) usageprovider.Provider {
	return AmpProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Amp provider id.
func (AmpProvider) Provider() usage.Provider { return usage.ProviderAmp }

// WithFileFilter returns an Amp provider that skips source files the filter
// rejects.
func (p AmpProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Amp usage entries, newest first.
func (p AmpProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadAmpEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Droid provider configured with data roots.
func (DroidProvider) WithPaths(paths []string) usageprovider.Provider {
	return DroidProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Droid provider id.
func (DroidProvider) Provider() usage.Provider { return usage.ProviderDroid }

// WithFileFilter returns a Droid provider that skips source files the filter
// rejects.
func (p DroidProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Droid usage entries, newest first.
func (p DroidProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadDroidEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Kilo provider configured with data roots.
func (KiloProvider) WithPaths(paths []string) usageprovider.Provider {
	return KiloProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Kilo provider id.
func (KiloProvider) Provider() usage.Provider { return usage.ProviderKilo }

// Entries loads normalized Kilo usage entries, newest first.
func (p KiloProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadKiloEntries(p.paths)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Hermes provider configured with data roots.
func (HermesProvider) WithPaths(paths []string) usageprovider.Provider {
	return HermesProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Hermes provider id.
func (HermesProvider) Provider() usage.Provider { return usage.ProviderHermes }

// Entries loads normalized Hermes Agent usage entries, newest first.
func (p HermesProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadHermesEntries(p.paths)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Codebuff provider configured with data roots.
func (CodebuffProvider) WithPaths(paths []string) usageprovider.Provider {
	return CodebuffProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Codebuff provider id.
func (CodebuffProvider) Provider() usage.Provider { return usage.ProviderCodebuff }

// WithFileFilter returns a Codebuff provider that skips source files the
// filter rejects.
func (p CodebuffProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized Codebuff usage entries, newest first.
func (p CodebuffProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadCodebuffEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns an OpenCode provider configured with data roots.
func (OpenCodeProvider) WithPaths(paths []string) usageprovider.Provider {
	return OpenCodeProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the OpenCode provider id.
func (OpenCodeProvider) Provider() usage.Provider { return usage.ProviderOpenCode }

// WithFileFilter returns an OpenCode provider that skips source files the
// filter rejects. The filter applies to message files only; the OpenCode
// database goes through the SQLite path and is always scanned.
func (p OpenCodeProvider) WithFileFilter(filter usage.FileFilter) usageprovider.Provider {
	p.filter = filter
	return p
}

// Entries loads normalized OpenCode usage entries, newest first.
func (p OpenCodeProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadOpenCodeEntries(p.paths, p.filter)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}

// WithPaths returns a Goose provider configured with data roots.
func (GooseProvider) WithPaths(paths []string) usageprovider.Provider {
	return GooseProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Goose provider id.
func (GooseProvider) Provider() usage.Provider { return usage.ProviderGoose }

// Entries loads normalized Goose usage entries, newest first.
func (p GooseProvider) Entries() ([]usage.Entry, error) {
	entries, err := loadGooseEntries(p.paths)
	if err != nil {
		return nil, err
	}
	return sortEntriesByTimestampDesc(entries), nil
}
