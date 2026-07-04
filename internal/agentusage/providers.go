package agentusage

import (
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

// CopilotProvider loads GitHub Copilot CLI usage entries.
type CopilotProvider struct{}

// GeminiProvider loads Gemini CLI usage entries.
type GeminiProvider struct{}

// KimiProvider loads Kimi usage entries.
type KimiProvider struct{}

// QwenProvider loads Qwen usage entries.
type QwenProvider struct{}

// OpenClawProvider loads OpenClaw usage entries.
type OpenClawProvider struct{}

// PiProvider loads pi-agent usage entries.
type PiProvider struct{}

// AmpProvider loads Amp usage entries.
type AmpProvider struct{}

var (
	_ usageprovider.Provider = CopilotProvider{}
	_ usageprovider.Provider = GeminiProvider{}
	_ usageprovider.Provider = KimiProvider{}
	_ usageprovider.Provider = QwenProvider{}
	_ usageprovider.Provider = OpenClawProvider{}
	_ usageprovider.Provider = PiProvider{}
	_ usageprovider.Provider = AmpProvider{}
)

// Provider returns the GitHub Copilot CLI provider id.
func (CopilotProvider) Provider() usage.Provider { return usage.ProviderCopilot }

// Entries loads normalized GitHub Copilot CLI usage entries.
func (CopilotProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadCopilotEntries(paths)
}

// Provider returns the Gemini CLI provider id.
func (GeminiProvider) Provider() usage.Provider { return usage.ProviderGemini }

// Entries loads normalized Gemini CLI usage entries.
func (GeminiProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadGeminiEntries(paths)
}

// Provider returns the Kimi provider id.
func (KimiProvider) Provider() usage.Provider { return usage.ProviderKimi }

// Entries loads normalized Kimi usage entries.
func (KimiProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadKimiEntries(paths)
}

// Provider returns the Qwen provider id.
func (QwenProvider) Provider() usage.Provider { return usage.ProviderQwen }

// Entries loads normalized Qwen usage entries.
func (QwenProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadQwenEntries(paths)
}

// Provider returns the OpenClaw provider id.
func (OpenClawProvider) Provider() usage.Provider { return usage.ProviderOpenClaw }

// Entries loads normalized OpenClaw usage entries.
func (OpenClawProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadOpenClawEntries(paths)
}

// Provider returns the pi-agent provider id.
func (PiProvider) Provider() usage.Provider { return usage.ProviderPi }

// Entries loads normalized pi-agent usage entries.
func (PiProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadPiEntries(paths)
}

// Provider returns the Amp provider id.
func (AmpProvider) Provider() usage.Provider { return usage.ProviderAmp }

// Entries loads normalized Amp usage entries.
func (AmpProvider) Entries(paths []string) ([]usage.Entry, error) {
	return loadAmpEntries(paths)
}
