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
		// Two events 30s apart: the gap counts, plus the day floor.
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
	if report.Daily[6].ActiveSeconds != 90 {
		t.Fatalf("30s gap plus the 60s floor must yield 90s, got %d", report.Daily[6].ActiveSeconds)
	}
	if report.Daily[5].ActiveSeconds != 60 {
		t.Fatalf("a lone event is worth the floor alone, got %d", report.Daily[5].ActiveSeconds)
	}
	if report.Totals.ActiveSeconds != 150 {
		t.Fatalf("want 150s across both days, got %d", report.Totals.ActiveSeconds)
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
	// Each group runs its own clock: tracklm's 30s gap plus its floor, other's
	// single event is its floor.
	if report.Projects[0].ActiveSeconds != 90 {
		t.Fatalf("tracklm active time: want 90s, got %d", report.Projects[0].ActiveSeconds)
	}
	if report.Projects[1].ActiveSeconds != 60 {
		t.Fatalf("other active time: want 60s, got %d", report.Projects[1].ActiveSeconds)
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
	if report.Daily[6].ActiveSeconds != 65 {
		t.Fatalf("the edit moves the clock like any event: want 65s, got %d", report.Daily[6].ActiveSeconds)
	}
}

// The idle rule is the server's: a gap within 15 minutes is active time, a
// longer one is a break and counts nothing. The database hands entries over in
// no particular order, so the walk must sort them itself.
func TestBuildIdleRuleMatchesTheServer(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.Local)
	start := now.Add(-3 * time.Hour)
	entries := []usage.Entry{
		entry(start.Add(15*time.Minute), "claude", "m", "p", 1),             // exactly the timeout: counts
		entry(start, "claude", "m", "p", 1),                                 // out of order on purpose
		entry(start.Add(30*time.Minute+time.Second), "claude", "m", "p", 1), // one second past: a break
		entry(start.Add(40*time.Minute), "claude", "m", "p", 1),             // 9m59s after the break: counts
	}

	report := Build(entries, 7, now)

	want := int64(15*60 + (9*60 + 59) + 60)
	if report.Daily[6].ActiveSeconds != want {
		t.Fatalf("active seconds = %d, want %d", report.Daily[6].ActiveSeconds, want)
	}
	if report.Totals.ActiveSeconds != want || report.Projects[0].ActiveSeconds != want {
		t.Fatalf("totals %d and project %d must agree with the day %d",
			report.Totals.ActiveSeconds, report.Projects[0].ActiveSeconds, want)
	}
	if entries[1].Timestamp != start {
		t.Fatal("Build reordered the caller's slice")
	}
}

// A day floor lands once per day, and once per group per day — never once per
// event, and never once for the whole window.
func TestBuildFloorIsPerDay(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.Local)
	entries := []usage.Entry{
		entry(now.Add(-time.Hour), "claude", "m", "p", 1),
		entry(now.AddDate(0, 0, -1), "claude", "m", "p", 1),
		entry(now.AddDate(0, 0, -2), "claude", "m", "p", 1),
	}

	report := Build(entries, 7, now)

	if report.Totals.ActiveSeconds != 180 {
		t.Fatalf("three lone days must be three floors, got %d", report.Totals.ActiveSeconds)
	}
	if report.Providers[0].ActiveSeconds != 180 {
		t.Fatalf("the provider spans the same three days, got %d", report.Providers[0].ActiveSeconds)
	}
}
