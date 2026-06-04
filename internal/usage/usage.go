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
	Provider    Provider   `json:"provider"`
	ID          string     `json:"id,omitempty"`
	SourceFile  string     `json:"source_file,omitempty"`
	SourceLine  int        `json:"source_line,omitempty"`
	SourceStart int64      `json:"source_start,omitempty"`
	SourceEnd   int64      `json:"source_end,omitempty"`
	Timestamp   time.Time  `json:"timestamp"`
	Date        string     `json:"date"`
	Project     string     `json:"project"`
	ProjectPath string     `json:"project_path,omitempty"`
	SessionID   string     `json:"session_id,omitempty"`
	Model       string     `json:"model,omitempty"`
	Usage       TokenUsage `json:"usage"`
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
