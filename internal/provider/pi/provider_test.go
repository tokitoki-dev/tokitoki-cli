package pi

import (
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

// TestLoadsEntry is the pi-agent smoke test: a minimal fixture must produce
// exactly one entry with the expected identity and token counts.
func TestLoadsEntry(t *testing.T) {
	entries, err := func() ([]usage.Entry, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "project-a", "agent_session-a.jsonl")
		providertest.WriteFile(t, path, `{"type":"message","timestamp":"2026-01-02T00:00:00.000Z","message":{"role":"assistant","model":"gpt-5","usage":{"totalTokens":333}}}`+"\n")
		return Provider{}.WithPaths([]string{dir}).Entries()
	}()

	providertest.AssertSingleEntry(t, entries, err, providertest.WantEntry{
		Provider:  usage.ProviderPi,
		Model:     "[pi] gpt-5",
		SessionID: "session-a",
		Project:   usage.UnknownProject,
		Tokens: usage.TokenUsage{
			OutputTokens: 333,
			TotalTokens:  333,
		},
	})
}

const (
	header   = `{"type":"session","version":3,"id":"pi_ses_001","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/Users/dev/workspace/demo"}`
	message1 = `{"type":"message","id":"msg_001","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","model":"claude-sonnet-4","provider":"anthropic","usage":{"input":100,"output":50,"cacheRead":10,"cacheWrite":5,"totalTokens":165}}}`
	message2 = `{"type":"message","id":"msg_002","parentId":"msg_001","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","model":"claude-sonnet-4","provider":"anthropic","usage":{"input":7,"output":3,"totalTokens":10}}}`
)

// TestReportsHeaderCWD: the session header's cwd becomes ProjectPath; the ID
// is the message's own and owes nothing to it.
func TestReportsHeaderCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "--Users-dev-workspace-demo--", "2026-01-01_pi_ses_001.jsonl")
	providertest.WriteFile(t, path, header+"\n"+message1+"\n")

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v, want 1", len(entries), err)
	}
	entry := entries[0]
	if entry.ProjectPath != "/Users/dev/workspace/demo" {
		t.Fatalf("project path = %q, want the header cwd", entry.ProjectPath)
	}
	if entry.Project != "--Users-dev-workspace-demo--" {
		t.Fatalf("project = %q, want the unchanged directory name", entry.Project)
	}
	if want := usageprovider.EventID(usage.ProviderPi, entry.SessionID, "msg_001"); entry.ID != want {
		t.Fatalf("ID = %s, want the message's own id %s", entry.ID, want)
	}
}

// TestResumedParseKeepsHeaderCWD: a scan resuming past the header must still
// attach its cwd, and yield the same ID a whole-file parse does.
func TestResumedParseKeepsHeaderCWD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "--Users-dev-workspace-demo--", "2026-01-01_pi_ses_001.jsonl")
	providertest.WriteFile(t, path, header+"\n"+message1+"\n"+message2+"\n")

	whole, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil || len(whole) != 2 {
		t.Fatalf("whole parse: entries = %d, err = %v, want 2", len(whole), err)
	}
	var resumed []usage.Entry
	resumeAt := int64(len(header) + 1 + len(message1) + 1)
	err = Provider{}.WithPaths([]string{dir}).(Provider).StreamEntries(
		func(string) int64 { return resumeAt },
		func(_ string, entries []usage.Entry, _ int64) error {
			resumed = append(resumed, entries...)
			return nil
		},
	)
	if err != nil || len(resumed) != 1 {
		t.Fatalf("resumed parse: entries = %d, err = %v, want 1", len(resumed), err)
	}
	if resumed[0].ProjectPath != "/Users/dev/workspace/demo" {
		t.Fatalf("project path = %q, want the header cwd", resumed[0].ProjectPath)
	}
	// Entries() returns newest first, so msg_002 leads.
	if resumed[0].ID != whole[0].ID {
		t.Fatalf("resumed ID = %s, want %s from the whole parse", resumed[0].ID, whole[0].ID)
	}
}

// TestNoHeaderKeepsDirectoryName: a session file without a header keeps the
// old directory-name ProjectPath, and the same ID it has with one.
func TestNoHeaderKeepsDirectoryName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "--Users-dev-workspace-demo--", "2026-01-01_pi_ses_001.jsonl")
	providertest.WriteFile(t, path, message1+"\n")

	entries, err := Provider{}.WithPaths([]string{dir}).Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v, want 1", len(entries), err)
	}
	if entries[0].ProjectPath != "--Users-dev-workspace-demo--" {
		t.Fatalf("project path = %q, want the old directory name", entries[0].ProjectPath)
	}
	if want := usageprovider.EventID(usage.ProviderPi, entries[0].SessionID, "msg_001"); entries[0].ID != want {
		t.Fatalf("ID = %s, want %s", entries[0].ID, want)
	}
}
