package agentusage

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

type providerBase struct {
	paths []string
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

// Entries loads normalized GitHub Copilot CLI usage entries.
func (p CopilotProvider) Entries() ([]usage.Entry, error) {
	return loadCopilotEntries(p.paths)
}

// WithPaths returns a Gemini CLI provider configured with data roots.
func (GeminiProvider) WithPaths(paths []string) usageprovider.Provider {
	return GeminiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Gemini CLI provider id.
func (GeminiProvider) Provider() usage.Provider { return usage.ProviderGemini }

// Entries loads normalized Gemini CLI usage entries.
func (p GeminiProvider) Entries() ([]usage.Entry, error) {
	return loadGeminiEntries(p.paths)
}

// WithPaths returns a Kimi provider configured with data roots.
func (KimiProvider) WithPaths(paths []string) usageprovider.Provider {
	return KimiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Kimi provider id.
func (KimiProvider) Provider() usage.Provider { return usage.ProviderKimi }

// Entries loads normalized Kimi usage entries.
func (p KimiProvider) Entries() ([]usage.Entry, error) {
	return loadKimiEntries(p.paths)
}

// WithPaths returns a Qwen provider configured with data roots.
func (QwenProvider) WithPaths(paths []string) usageprovider.Provider {
	return QwenProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Qwen provider id.
func (QwenProvider) Provider() usage.Provider { return usage.ProviderQwen }

// Entries loads normalized Qwen usage entries.
func (p QwenProvider) Entries() ([]usage.Entry, error) {
	return loadQwenEntries(p.paths)
}

// WithPaths returns an OpenClaw provider configured with data roots.
func (OpenClawProvider) WithPaths(paths []string) usageprovider.Provider {
	return OpenClawProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the OpenClaw provider id.
func (OpenClawProvider) Provider() usage.Provider { return usage.ProviderOpenClaw }

// Entries loads normalized OpenClaw usage entries.
func (p OpenClawProvider) Entries() ([]usage.Entry, error) {
	return loadOpenClawEntries(p.paths)
}

// WithPaths returns a pi-agent provider configured with data roots.
func (PiProvider) WithPaths(paths []string) usageprovider.Provider {
	return PiProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the pi-agent provider id.
func (PiProvider) Provider() usage.Provider { return usage.ProviderPi }

// Entries loads normalized pi-agent usage entries.
func (p PiProvider) Entries() ([]usage.Entry, error) {
	return loadPiEntries(p.paths)
}

// WithPaths returns an Amp provider configured with data roots.
func (AmpProvider) WithPaths(paths []string) usageprovider.Provider {
	return AmpProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Amp provider id.
func (AmpProvider) Provider() usage.Provider { return usage.ProviderAmp }

// Entries loads normalized Amp usage entries.
func (p AmpProvider) Entries() ([]usage.Entry, error) {
	return loadAmpEntries(p.paths)
}

// WithPaths returns a Droid provider configured with data roots.
func (DroidProvider) WithPaths(paths []string) usageprovider.Provider {
	return DroidProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Droid provider id.
func (DroidProvider) Provider() usage.Provider { return usage.ProviderDroid }

// Entries loads normalized Droid usage entries.
func (p DroidProvider) Entries() ([]usage.Entry, error) {
	return loadDroidEntries(p.paths)
}

// WithPaths returns a Kilo provider configured with data roots.
func (KiloProvider) WithPaths(paths []string) usageprovider.Provider {
	return KiloProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Kilo provider id.
func (KiloProvider) Provider() usage.Provider { return usage.ProviderKilo }

// Entries loads normalized Kilo usage entries.
func (p KiloProvider) Entries() ([]usage.Entry, error) {
	return loadKiloEntries(p.paths)
}

// WithPaths returns a Hermes provider configured with data roots.
func (HermesProvider) WithPaths(paths []string) usageprovider.Provider {
	return HermesProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Hermes provider id.
func (HermesProvider) Provider() usage.Provider { return usage.ProviderHermes }

// Entries loads normalized Hermes Agent usage entries.
func (p HermesProvider) Entries() ([]usage.Entry, error) {
	return loadHermesEntries(p.paths)
}

// WithPaths returns a Codebuff provider configured with data roots.
func (CodebuffProvider) WithPaths(paths []string) usageprovider.Provider {
	return CodebuffProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Codebuff provider id.
func (CodebuffProvider) Provider() usage.Provider { return usage.ProviderCodebuff }

// Entries loads normalized Codebuff usage entries.
func (p CodebuffProvider) Entries() ([]usage.Entry, error) {
	return loadCodebuffEntries(p.paths)
}

// WithPaths returns an OpenCode provider configured with data roots.
func (OpenCodeProvider) WithPaths(paths []string) usageprovider.Provider {
	return OpenCodeProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the OpenCode provider id.
func (OpenCodeProvider) Provider() usage.Provider { return usage.ProviderOpenCode }

// Entries loads normalized OpenCode usage entries.
func (p OpenCodeProvider) Entries() ([]usage.Entry, error) {
	return loadOpenCodeEntries(p.paths)
}

// WithPaths returns a Goose provider configured with data roots.
func (GooseProvider) WithPaths(paths []string) usageprovider.Provider {
	return GooseProvider{providerBase: newProviderBase(paths)}
}

// Provider returns the Goose provider id.
func (GooseProvider) Provider() usage.Provider { return usage.ProviderGoose }

// Entries loads normalized Goose usage entries.
func (p GooseProvider) Entries() ([]usage.Entry, error) {
	return loadGooseEntries(p.paths)
}
