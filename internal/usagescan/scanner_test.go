package usagescan

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usageprovider"
)

func TestScanInsertsBuiltInProviderEntries(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, "codex")
	sessionDir := filepath.Join(codexDir, "sessions", "2026", "06", "04")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sessionDir, "rollout-session-a.jsonl"),
		[]byte(
			`{"timestamp":"2026-06-04T01:02:03Z","type":"session_meta","payload":{"id":"session-a","cwd":"/Users/me/workspace/tokitoki"}}`+"\n"+
				`{"timestamp":"2026-06-04T01:02:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	scanner := New(db)
	result, err := scanner.Scan(map[usage.Provider][]string{
		usage.ProviderCodex: []string{codexDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	codexResult := result.Providers[usage.ProviderCodex]
	if codexResult.EventsInserted != 1 {
		t.Fatalf("first events inserted = %d, want 1", codexResult.EventsInserted)
	}

	result, err = scanner.Scan(map[usage.Provider][]string{
		usage.ProviderCodex: []string{codexDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	codexResult = result.Providers[usage.ProviderCodex]
	if codexResult.EventsInserted != 0 {
		t.Fatalf("second events inserted = %d, want 0", codexResult.EventsInserted)
	}
}

func TestScanUsesRegisteredProvider(t *testing.T) {
	dir := t.TempDir()
	db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	provider := fakeProvider{
		provider: usage.Provider("fixture"),
		entries: []usage.Entry{{
			Provider:  usage.Provider("fixture"),
			ID:        "fixture-event",
			Timestamp: time.Date(2026, 6, 4, 1, 2, 3, 0, time.UTC),
			Date:      "2026-06-04",
			Project:   "tracklm",
			Language:  usage.UnknownLanguage,
			Usage: usage.TokenUsage{
				InputTokens:  1,
				OutputTokens: 2,
				TotalTokens:  3,
			},
		}},
	}

	scanner := New(db, &provider)
	result, err := scanner.Scan(map[usage.Provider][]string{
		provider.provider: []string{dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.paths) != 1 || provider.paths[0] != dir {
		t.Fatalf("provider paths = %#v, want scan dir", provider.paths)
	}
	providerResult := result.Providers[provider.provider]
	if providerResult.EventsInserted != 1 {
		t.Fatalf("events inserted = %d, want 1", providerResult.EventsInserted)
	}
}

type fakeProvider struct {
	provider usage.Provider
	entries  []usage.Entry
	paths    []string
}

func (p fakeProvider) Provider() usage.Provider {
	return p.provider
}

func (p *fakeProvider) WithPaths(paths []string) usageprovider.Provider {
	p.paths = append([]string{}, paths...)
	return p
}

func (p *fakeProvider) Entries() ([]usage.Entry, error) {
	return p.entries, nil
}
