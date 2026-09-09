package usagestats

import (
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func entry(ts time.Time, provider, model, project string, tokens uint64) usage.Entry {
	return usage.Entry{
		Provider:  usage.Provider(provider),
		Timestamp: ts,
		Model:     model,
		Project:   project,
		Usage:     usage.TokenUsage{InputTokens: tokens / 2, OutputTokens: tokens / 2, TotalTokens: tokens},
	}
}

func TestBuild(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.Local)
	entries := []usage.Entry{
		// Two events in the same 2-minute bucket: one bucket of active time.
		entry(now.Add(-1*time.Hour), "claude", "claude-fable-5", "tracklm", 1000),
		entry(now.Add(-1*time.Hour).Add(30*time.Second), "claude", "claude-fable-5", "tracklm", 500),
		// Yesterday, different provider; heartbeats carry zero tokens.
		entry(now.AddDate(0, 0, -1), "codex", "", "other", 0),
		// Outside the window: must be ignored.
		entry(now.AddDate(0, 0, -10), "claude", "claude-fable-5", "tracklm", 9999),
	}

	report := Build(entries, 7, now)

	if report.Days != 7 || len(report.Daily) != 7 {
		t.Fatalf("want dense 7-day window, got days=%d len=%d", report.Days, len(report.Daily))
	}
	if report.Daily[0].Date != "2026-08-03" || report.Daily[6].Date != "2026-08-09" {
		t.Fatalf("window misaligned: %s .. %s", report.Daily[0].Date, report.Daily[6].Date)
	}
	if report.Totals.Events != 3 {
		t.Fatalf("out-of-window entry counted: events=%d", report.Totals.Events)
	}
	if report.Totals.TotalTokens != 1500 {
		t.Fatalf("want 1500 tokens, got %d", report.Totals.TotalTokens)
	}
	if report.Daily[6].TotalTokens != 1500 || report.Daily[6].Events != 2 {
		t.Fatalf("today misaggregated: %+v", report.Daily[6])
	}
	if report.Daily[6].ActiveSeconds != 120 {
		t.Fatalf("two events in one bucket must yield 120s, got %d", report.Daily[6].ActiveSeconds)
	}
	if report.Totals.ActiveSeconds != 240 {
		t.Fatalf("want 240s across both days, got %d", report.Totals.ActiveSeconds)
	}
	if len(report.Providers) != 2 || report.Providers[0].Name != "claude" {
		t.Fatalf("providers wrong: %+v", report.Providers)
	}
	// The codex entry has no model; empty names never form a group.
	if len(report.Models) != 1 || report.Models[0].Name != "claude-fable-5" {
		t.Fatalf("models wrong: %+v", report.Models)
	}
	if len(report.Projects) != 2 || report.Projects[0].Name != "tracklm" {
		t.Fatalf("projects wrong: %+v", report.Projects)
	}
	// tracklm's two events share one bucket; other has its own bucket.
	if report.Projects[0].ActiveSeconds != 120 {
		t.Fatalf("tracklm active time: want 120s, got %d", report.Projects[0].ActiveSeconds)
	}
	if report.Projects[1].ActiveSeconds != 120 {
		t.Fatalf("other active time: want 120s, got %d", report.Projects[1].ActiveSeconds)
	}
	if report.Project != nil {
		t.Fatalf("plain Build must not nest a project report")
	}
}

func TestBuildForProject(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.Local)
	entries := []usage.Entry{
		entry(now.Add(-1*time.Hour), "claude", "claude-fable-5", "tracklm", 1000),
		entry(now.AddDate(0, 0, -1), "codex", "", "other", 500),
	}

	report := BuildForProject(entries, 7, now, "tracklm")
	if report.Totals.Events != 2 {
		t.Fatalf("outer report must stay global: events=%d", report.Totals.Events)
	}
	if report.Project == nil {
		t.Fatal("project sub-report missing")
	}
	if report.Project.Totals.Events != 1 || report.Project.Totals.TotalTokens != 1000 {
		t.Fatalf("sub-report not scoped: %+v", report.Project.Totals)
	}
	if report.Project.Project != nil {
		t.Fatal("sub-report must not nest further")
	}

	// An unknown project still yields a zero-filled sub-report, not nil.
	empty := BuildForProject(entries, 7, now, "no-such-project")
	if empty.Project == nil || empty.Project.Totals.Events != 0 {
		t.Fatalf("unknown project must yield an empty sub-report, got %+v", empty.Project)
	}
	if len(empty.Project.Daily) != 7 {
		t.Fatalf("empty sub-report must keep the dense daily window, got %d", len(empty.Project.Daily))
	}
}

// A file edit is a side effect of a request already counted. Its lines and
// activity belong in the report; the request count does not include it.
func TestBuildDoesNotCountFileEditsAsEvents(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.Local)
	call := entry(now.Add(-time.Hour), "claude", "claude-fable-5", "tracklm", 1000)
	edit := usage.Entry{
		Provider:   usage.ProviderClaude,
		EventKind:  usage.EventKindFileEdit,
		Timestamp:  now.Add(-time.Hour).Add(5 * time.Second),
		Project:    "tracklm",
		LinesAdded: 12,
	}

	report := Build([]usage.Entry{call, edit}, 7, now)

	if report.Totals.Events != 1 || report.Daily[6].Events != 1 {
		t.Fatalf("file edit counted as a request: totals=%d today=%d", report.Totals.Events, report.Daily[6].Events)
	}
	if report.Totals.TotalTokens != 1000 {
		t.Fatalf("tokens = %d, want 1000", report.Totals.TotalTokens)
	}
	if len(report.Projects) != 1 || report.Projects[0].Events != 1 {
		t.Fatalf("project group counted the edit: %+v", report.Projects)
	}
	if report.Daily[6].ActiveSeconds != 120 {
		t.Fatalf("edit in the same bucket must not add activity: %d", report.Daily[6].ActiveSeconds)
	}
}
