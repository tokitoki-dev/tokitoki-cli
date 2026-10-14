package usagescan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestScanSkipsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, "codex")
	sessionDir := filepath.Join(codexDir, "sessions", "2026", "06", "04")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "rollout-session-a.jsonl")
	if err := os.WriteFile(
		sessionFile,
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
	dirs := map[usage.Provider][]string{usage.ProviderCodex: {codexDir}}

	result, err := scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if parsed := result.Providers[usage.ProviderCodex].EventsParsed; parsed != 1 {
		t.Fatalf("first events parsed = %d, want 1", parsed)
	}

	result, err = scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if parsed := result.Providers[usage.ProviderCodex].EventsParsed; parsed != 0 {
		t.Fatalf("unchanged file events parsed = %d, want 0", parsed)
	}

	appended, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appended.WriteString(
		`{"timestamp":"2026-06-04T01:02:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}}}` + "\n",
	); err != nil {
		t.Fatal(err)
	}
	if err := appended.Close(); err != nil {
		t.Fatal(err)
	}

	result, err = scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	codexResult := result.Providers[usage.ProviderCodex]
	if codexResult.EventsParsed != 2 {
		t.Fatalf("appended file events parsed = %d, want 2", codexResult.EventsParsed)
	}
	if codexResult.EventsInserted != 1 {
		t.Fatalf("appended file events inserted = %d, want 1", codexResult.EventsInserted)
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
		filepath.Join(projectDir, ".tokitoki"),
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

// An agent's log records the branch of the folder it was started in. Started
// in a folder of repositories, that is no branch at all ("HEAD"), and the work
// is in one of the repositories: each event takes the branch that repository
// had checked out when the event happened, whatever the provider said.
func TestResolveProjectsTakesEachEventsBranchFromItsCheckout(t *testing.T) {
	workspace := t.TempDir()
	repo := filepath.Join(workspace, "tracklm-nextjs")
	switched := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for path, contents := range map[string]string{
		filepath.Join(repo, ".git", "HEAD"): "ref: refs/heads/feature\n",
		filepath.Join(repo, ".git", "logs", "HEAD"): fmt.Sprintf(
			"%[1]s %[1]s Dev <dev@example.com> %[2]d +0000\tcommit (initial): first\n"+
				"%[1]s %[1]s Dev <dev@example.com> %[3]d +0000\tcheckout: moving from main to feature\n",
			strings.Repeat("a", 40), switched.Add(-24*time.Hour).Unix(), switched.Unix()),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entity := filepath.Join(repo, "app", "page.tsx")
	entries := []usage.Entry{
		{ID: "before", ProjectPath: workspace, Entity: entity, Branch: "HEAD", Timestamp: switched.Add(-time.Hour)},
		{ID: "after", ProjectPath: workspace, Entity: entity, Branch: "HEAD", Timestamp: switched.Add(time.Hour)},
		{ID: "folder", ProjectPath: workspace, Branch: "HEAD", Timestamp: switched.Add(time.Hour)},
	}
	(&Scanner{}).resolveProjects(entries)
	for i, want := range []string{"main", "feature", ""} {
		if entries[i].Branch != want {
			t.Errorf("%s branch = %q, want %q", entries[i].ID, entries[i].Branch, want)
		}
	}
}

// One agent session that cd'd around a repository is one project, and the
// event IDs the provider built stay exactly as they were.
func TestResolveProjectsFoldsSubfoldersIntoTheirRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "cps-dev")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries := []usage.Entry{
		{ID: "root", Project: "cps-dev", ProjectPath: repo},
		{ID: "web", Project: "web", ProjectPath: filepath.Join(repo, "cps-web", "apps", "web")},
		{ID: "edit", Project: "src", ProjectPath: filepath.Join(repo, "src"), Entity: filepath.Join(repo, "gateway", "main.py")},
	}
	(&Scanner{}).resolveProjects(entries)
	for i, id := range []string{"root", "web", "edit"} {
		if entries[i].ID != id || entries[i].Project != "cps-dev" || entries[i].ProjectPath != repo {
			t.Fatalf("entry %d = %q %q %q, want %q in cps-dev at %q", i, entries[i].ID, entries[i].Project, entries[i].ProjectPath, id, repo)
		}
	}
}

// Providers that know no folder keep the identity they always reported.
func TestResolveProjectsKeepsFolderlessProviderIdentity(t *testing.T) {
	entries := []usage.Entry{{ID: "amp", Project: "amp", ProjectPath: "Amp"}}
	(&Scanner{}).resolveProjects(entries)
	if entries[0].Project != "amp" || entries[0].ProjectPath != "Amp" {
		t.Fatalf("identity = %q %q, want amp/Amp unchanged", entries[0].Project, entries[0].ProjectPath)
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
