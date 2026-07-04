package agentusage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/labx/tokitoki-agent/internal/usage"
)

func TestProvidersLoadEntries(t *testing.T) {
	tests := []struct {
		name      string
		provider  func() ([]usage.Entry, error)
		want      usage.Provider
		model     string
		sessionID string
		project   string
		tokens    usage.TokenUsage
	}{
		{
			name: "copilot",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "copilot.jsonl")
				writeFile(t, path, `{"type":"span","traceId":"trace-1","spanId":"span-1","name":"chat claude-sonnet-4","endTime":[1775934264,967317833],"attributes":{"gen_ai.operation.name":"chat","gen_ai.response.model":"claude-sonnet-4","gen_ai.conversation.id":"conv-1","gen_ai.usage.input_tokens":19452,"gen_ai.usage.output_tokens":281,"gen_ai.usage.cache_read.input_tokens":123,"gen_ai.usage.cache_creation.input_tokens":25,"gen_ai.usage.reasoning.output_tokens":128}}}`+"\n")
				return CopilotProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderCopilot,
			model:     "claude-sonnet-4",
			sessionID: "conv-1",
			project:   "copilot",
			tokens: usage.TokenUsage{
				InputTokens:              19329,
				OutputTokens:             281,
				CacheCreationInputTokens: 25,
				CacheReadInputTokens:     123,
				ReasoningOutputTokens:    128,
				TotalTokens:              19886,
			},
		},
		{
			name: "gemini",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "session-a.jsonl")
				writeFile(t, path,
					`{"sessionId":"session-a","projectHash":"project-a","startTime":"2026-05-17T11:07:00.000Z"}`+"\n"+
						`{"id":"msg-a","timestamp":"2026-05-17T11:07:32.000Z","type":"gemini","model":"gemini-3-flash-preview","tokens":{"input":15327,"output":23,"cached":11526,"thoughts":919,"tool":7,"total":16276}}`+"\n")
				return GeminiProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderGemini,
			model:     "gemini-3-flash-preview",
			sessionID: "session-a",
			project:   "gemini",
			tokens: usage.TokenUsage{
				InputTokens:           3808,
				OutputTokens:          23,
				CacheReadInputTokens:  11526,
				ReasoningOutputTokens: 919,
				TotalTokens:           16276,
			},
		},
		{
			name: "kimi",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "config.json"), `{"model":"kimi-k2"}`)
				path := filepath.Join(dir, "sessions", "group", "session-a", "wire.jsonl")
				writeFile(t, path,
					`{"type":"metadata","protocol_version":"1.3"}`+"\n"+
						`{"timestamp":1770983427.123,"message":{"type":"StatusUpdate","payload":{"token_usage":{"input_other":100,"output":50,"input_cache_read":10,"input_cache_creation":20},"message_id":"msg-1"}}}`+"\n")
				return KimiProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderKimi,
			model:     "kimi-k2",
			sessionID: "session-a",
			project:   "kimi",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				TotalTokens:              180,
			},
		},
		{
			name: "qwen",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "projects", "project-a", "chats", "chat-a.jsonl")
				writeFile(t, path, `{"type":"assistant","timestamp":"2026-01-02T00:00:00.000Z","sessionId":"session-a","model":"qwen3-coder","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":5,"cachedContentTokenCount":3,"totalTokenCount":38}}`+"\n")
				return QwenProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderQwen,
			model:     "qwen3-coder",
			sessionID: "session-a",
			project:   "qwen",
			tokens: usage.TokenUsage{
				InputTokens:           10,
				OutputTokens:          20,
				CacheReadInputTokens:  3,
				ReasoningOutputTokens: 5,
				TotalTokens:           38,
			},
		},
		{
			name: "openclaw",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "agents", "main", "sessions", "abc.jsonl")
				writeFile(t, path,
					`{"type":"model_change","provider":"openai-codex","modelId":"gpt-5.2"}`+"\n"+
						`{"type":"message","message":{"role":"assistant","usage":{"input":1660,"output":55,"cacheRead":108928},"timestamp":1769753935279}}`+"\n")
				return OpenClawProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderOpenClaw,
			model:     "[openclaw] gpt-5.2",
			sessionID: "abc",
			project:   "openclaw",
			tokens: usage.TokenUsage{
				InputTokens:          1660,
				OutputTokens:         55,
				CacheReadInputTokens: 108928,
				TotalTokens:          110643,
			},
		},
		{
			name: "pi",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "project-a", "agent_session-a.jsonl")
				writeFile(t, path, `{"type":"message","timestamp":"2026-01-02T00:00:00.000Z","message":{"role":"assistant","model":"gpt-5","usage":{"totalTokens":333}}}`+"\n")
				return PiProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderPi,
			model:     "[pi] gpt-5",
			sessionID: "session-a",
			project:   "unknown",
			tokens: usage.TokenUsage{
				OutputTokens: 333,
				TotalTokens:  333,
			},
		},
		{
			name: "amp",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "threads", "thread.json")
				writeFile(t, path, `{"id":"thread-a","usageLedger":{"events":[{"id":"event-a","timestamp":"2026-01-02T00:00:00.000Z","model":"gpt-5","tokens":{"input":1,"output":2}}]}}`)
				return AmpProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderAmp,
			model:     "gpt-5",
			sessionID: "thread-a",
			project:   "amp",
			tokens: usage.TokenUsage{
				InputTokens:  1,
				OutputTokens: 2,
				TotalTokens:  3,
			},
		},
		{
			name: "droid",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "session-a.settings.json")
				writeFile(t, path, `{"model":"Claude-Sonnet-4-[Anthropic]","providerLock":"anthropic","providerLockTimestamp":"2026-01-02T00:00:00.000Z","tokenUsage":{"inputTokens":100,"outputTokens":50,"cacheCreationTokens":20,"cacheReadTokens":10,"thinkingTokens":5}}`)
				return DroidProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderDroid,
			model:     "claude-sonnet-4",
			sessionID: "session-a",
			project:   "droid",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				ReasoningOutputTokens:    5,
				TotalTokens:              185,
			},
		},
		{
			name: "kilo",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "kilo.db")
				db := openTestSQLite(t, dbPath)
				defer db.Close()
				execSQL(t, db, "CREATE TABLE message (id TEXT, session_id TEXT, data TEXT)")
				execSQL(t, db, `INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`,
					"row-1",
					"session-a",
					`{"id":"msg-1","role":"assistant","providerID":"anthropic","modelID":"claude-sonnet-4-20250514","time":{"created":1767312000000},"tokens":{"input":100,"output":50,"reasoning":5,"cache":{"read":10,"write":20}}}`,
				)
				return KiloProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderKilo,
			model:     "claude-sonnet-4-20250514",
			sessionID: "session-a",
			project:   "kilo",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				ReasoningOutputTokens:    5,
				TotalTokens:              185,
			},
		},
		{
			name: "hermes",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "state.db")
				db := openTestSQLite(t, dbPath)
				defer db.Close()
				execSQL(t, db, `CREATE TABLE sessions (
					id TEXT PRIMARY KEY,
					model TEXT,
					billing_provider TEXT,
					started_at REAL,
					message_count INTEGER,
					input_tokens INTEGER,
					output_tokens INTEGER,
					cache_read_tokens INTEGER,
					cache_write_tokens INTEGER,
					reasoning_tokens INTEGER,
					estimated_cost_usd REAL,
					actual_cost_usd REAL
				)`)
				execSQL(t, db, `INSERT INTO sessions (
					id, model, billing_provider, started_at, message_count, input_tokens,
					output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
					estimated_cost_usd, actual_cost_usd
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					"session-a", "gpt-5.5", "openai", 1750000000.25, 42, 100, 50, 10, 20, 5, 0.12, 0.34,
				)
				return HermesProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderHermes,
			model:     "gpt-5.5",
			sessionID: "session-a",
			project:   "hermes",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				ReasoningOutputTokens:    5,
				TotalTokens:              185,
			},
		},
		{
			name: "codebuff",
			provider: func() ([]usage.Entry, error) {
				root := filepath.Join(t.TempDir(), "manicode")
				path := filepath.Join(root, "projects", "project-a", "chats", "2026-01-02T03-04-05.000Z", "chat-messages.json")
				writeFile(t, path, `[{"id":"assistant-message","role":"assistant","timestamp":"2026-01-02T03:04:06.000Z","metadata":{"model":"claude-sonnet-4-20250514","usage":{"inputTokens":100,"outputTokens":50,"cacheCreationInputTokens":20,"cacheReadInputTokens":10}}}]`)
				return CodebuffProvider{}.WithPaths([]string{root}).Entries()
			},
			want:      usage.ProviderCodebuff,
			model:     "claude-sonnet-4-20250514",
			sessionID: "manicode/project-a/2026-01-02T03-04-05.000Z",
			project:   "codebuff",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				TotalTokens:              180,
			},
		},
		{
			name: "opencode",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				path := filepath.Join(dir, "storage", "message", "session-a", "msg-1.json")
				writeFile(t, path, `{"id":"msg-1","sessionID":"session-a","providerID":"anthropic","modelID":"claude-sonnet-4-20250514","time":{"created":1767312000000},"tokens":{"input":100,"output":50,"cache":{"read":10,"write":20}},"cost":0}`)
				return OpenCodeProvider{}.WithPaths([]string{dir}).Entries()
			},
			want:      usage.ProviderOpenCode,
			model:     "claude-sonnet-4-20250514",
			sessionID: "session-a",
			project:   "opencode",
			tokens: usage.TokenUsage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheCreationInputTokens: 20,
				CacheReadInputTokens:     10,
				TotalTokens:              180,
			},
		},
		{
			name: "goose",
			provider: func() ([]usage.Entry, error) {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "sessions.db")
				db := openTestSQLite(t, dbPath)
				defer db.Close()
				execSQL(t, db, `CREATE TABLE sessions (
					id TEXT PRIMARY KEY,
					model_config_json TEXT,
					provider_name TEXT,
					created_at TEXT,
					total_tokens INTEGER,
					input_tokens INTEGER,
					output_tokens INTEGER,
					accumulated_total_tokens INTEGER,
					accumulated_input_tokens INTEGER,
					accumulated_output_tokens INTEGER
				)`)
				execSQL(t, db, `INSERT INTO sessions (
					id, model_config_json, provider_name, created_at,
					accumulated_total_tokens, accumulated_input_tokens, accumulated_output_tokens
				) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"session-a", `{"model_name":"claude-sonnet-4-20250514"}`, "anthropic", "2026-05-01 01:02:03", 180, 100, 50,
				)
				return GooseProvider{}.WithPaths([]string{dbPath}).Entries()
			},
			want:      usage.ProviderGoose,
			model:     "claude-sonnet-4-20250514",
			sessionID: "session-a",
			project:   "goose",
			tokens: usage.TokenUsage{
				InputTokens:           100,
				OutputTokens:          50,
				ReasoningOutputTokens: 30,
				TotalTokens:           180,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := tt.provider()
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1: %#v", len(entries), entries)
			}
			entry := entries[0]
			if entry.Provider != tt.want {
				t.Fatalf("provider = %q, want %q", entry.Provider, tt.want)
			}
			if entry.Model != tt.model {
				t.Fatalf("model = %q, want %q", entry.Model, tt.model)
			}
			if entry.SessionID != tt.sessionID {
				t.Fatalf("session id = %q, want %q", entry.SessionID, tt.sessionID)
			}
			if entry.Project != tt.project {
				t.Fatalf("project = %q, want %q", entry.Project, tt.project)
			}
			if entry.Usage != tt.tokens {
				t.Fatalf("usage = %#v, want %#v", entry.Usage, tt.tokens)
			}
			if entry.ID == "" {
				t.Fatal("ID is empty")
			}
		})
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func openTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func execSQL(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}
