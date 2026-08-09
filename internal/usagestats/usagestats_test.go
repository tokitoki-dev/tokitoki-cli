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
}
