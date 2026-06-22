package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

const UnknownLanguage = "Unknown"

type TokenUsage struct {
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens,omitempty"`
	CachedInputTokens        uint64 `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64 `json:"total_tokens"`
}

type Entry struct {
	Provider    Provider  `json:"provider"`
	ID          string    `json:"id,omitempty"`
	SourceFile  string    `json:"source_file,omitempty"`
	SourceLine  int       `json:"source_line,omitempty"`
	SourceStart int64     `json:"source_start,omitempty"`
	SourceEnd   int64     `json:"source_end,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Date        string    `json:"date"`
	Project     string    `json:"project"`
	ProjectPath string    `json:"project_path,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Model       string    `json:"model,omitempty"`
	Language    string    `json:"language"`
	// OS is the operating system of the machine that produced this entry,
	// e.g. "macOS", "Windows", "Linux".
	OS string `json:"os,omitempty"`
	// Client is the human-readable IDE or app source the request came from.
	// VS Code plugins are normalized across providers, but standalone apps
	// remain product-specific, e.g. "VS Code", "Codex Desktop", "Claude CLI".
	Client string     `json:"client,omitempty"`
	Usage  TokenUsage `json:"usage"`
}

// NormalizeOS maps a Go runtime.GOOS value to a human-readable name.
func NormalizeOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return goos
	}
}

// NormalizeClient maps a provider-specific source token (Claude's "entrypoint"
// or Codex's "originator") to the real IDE/app source. VS Code plugins should
// not split by agent; standalone CLI/Desktop/SDK sources should remain
// product-specific.
// Unknown tokens are returned as-is so we never lose information; "" stays "".
func NormalizeClient(provider Provider, raw string) string {
	token := strings.ToLower(strings.TrimSpace(raw))
	if token == "" {
		return ""
	}
	switch provider {
	case ProviderClaude:
		switch token {
		case "claude-vscode":
			return "VS Code"
		case "claude-desktop":
			return "Claude Desktop"
		case "sdk-cli", "cli":
			return "Claude CLI"
		case "sdk-ts", "sdk-py", "sdk-python":
			return "Claude SDK"
		}
	case ProviderCodex:
		switch token {
		case "codex_vscode":
			return "VS Code"
		case "codex desktop", "codex-desktop":
			return "Codex Desktop"
		case "codex_cli_rs", "codex_cli", "cli":
			return "Codex CLI"
		}
	}
	return strings.TrimSpace(raw)
}

func NormalizeLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return UnknownLanguage
	}
	return language
}

func StableID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

type DailyProjectSummary struct {
	Provider                 Provider `json:"provider"`
	Date                     string   `json:"date"`
	Project                  string   `json:"project"`
	ProjectPath              string   `json:"project_path,omitempty"`
	InputTokens              uint64   `json:"input_tokens"`
	OutputTokens             uint64   `json:"output_tokens"`
	CacheCreationInputTokens uint64   `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64   `json:"cache_read_input_tokens,omitempty"`
	CachedInputTokens        uint64   `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens    uint64   `json:"reasoning_output_tokens,omitempty"`
	TotalTokens              uint64   `json:"total_tokens"`
}

func SummarizeDailyProjects(entries []Entry) []DailyProjectSummary {
	type key struct {
		provider Provider
		date     string
		project  string
		path     string
	}
	indexes := map[key]int{}
	summaries := make([]DailyProjectSummary, 0)
	for _, entry := range entries {
		key := key{
			provider: entry.Provider,
			date:     entry.Date,
			project:  entry.Project,
			path:     entry.ProjectPath,
		}
		index, ok := indexes[key]
		if !ok {
			index = len(summaries)
			indexes[key] = index
			summaries = append(summaries, DailyProjectSummary{
				Provider:    entry.Provider,
				Date:        entry.Date,
				Project:     entry.Project,
				ProjectPath: entry.ProjectPath,
			})
		}
		summary := &summaries[index]
		summary.InputTokens += entry.Usage.InputTokens
		summary.OutputTokens += entry.Usage.OutputTokens
		summary.CacheCreationInputTokens += entry.Usage.CacheCreationInputTokens
		summary.CacheReadInputTokens += entry.Usage.CacheReadInputTokens
		summary.CachedInputTokens += entry.Usage.CachedInputTokens
		summary.ReasoningOutputTokens += entry.Usage.ReasoningOutputTokens
		summary.TotalTokens += entry.Usage.TotalTokens
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Provider != summaries[j].Provider {
			return summaries[i].Provider < summaries[j].Provider
		}
		if summaries[i].Project != summaries[j].Project {
			return summaries[i].Project < summaries[j].Project
		}
		return summaries[i].Date < summaries[j].Date
	})
	return summaries
}
