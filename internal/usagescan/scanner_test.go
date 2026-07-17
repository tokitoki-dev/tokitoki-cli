package usagescan

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
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

func TestScanAppliesProjectFileToAgentEvents(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "checkout-folder")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, ".tokitoki-project"),
		[]byte("shared-ai-and-ide-name\nrelease\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	provider := fakeProvider{
		provider: usage.Provider("fixture"),
		entries: []usage.Entry{{
			Provider:    usage.Provider("fixture"),
			ID:          "project-file-event",
			Timestamp:   time.Now().UTC().Add(-time.Minute),
			Date:        time.Now().UTC().Format("2006-01-02"),
			Project:     "provider-name",
			ProjectPath: projectDir,
			Branch:      "provider-branch",
			Language:    usage.UnknownLanguage,
		}},
	}
	if _, err := New(db, &provider).Scan(map[usage.Provider][]string{
		provider.provider: {dir},
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := db.PendingEvents(time.Now().UTC(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending events = %d, want 1", len(pending))
	}
	entry := pending[0]
	if entry.Project != "shared-ai-and-ide-name" {
		t.Fatalf("project = %q, want shared-ai-and-ide-name", entry.Project)
	}
	if entry.ProjectPath != projectDir {
		t.Fatalf("project path = %q, want %q", entry.ProjectPath, projectDir)
	}
	if entry.Branch != "release" {
		t.Fatalf("branch = %q, want release", entry.Branch)
	}
}

func TestApplyProjectFilesPreservesPerEventBranchWithoutOverride(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, ".tokitoki-project"),
		[]byte("shared-name\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	entries := []usage.Entry{
		{ID: "one", ProjectPath: projectDir, Branch: "main"},
		{ID: "two", ProjectPath: projectDir, Branch: "feature"},
	}
	(&Scanner{}).applyProjectFiles(entries)
	if entries[0].Project != "shared-name" || entries[1].Project != "shared-name" {
		t.Fatalf("projects = %q/%q, want shared-name", entries[0].Project, entries[1].Project)
	}
	if entries[0].Branch != "main" || entries[1].Branch != "feature" {
		t.Fatalf("branches = %q/%q, want preserved", entries[0].Branch, entries[1].Branch)
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
