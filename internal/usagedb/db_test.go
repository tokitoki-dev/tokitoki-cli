package usagedb

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/labx/tracklm-goagent/internal/usage"
)

func TestInsertEventsIgnoresDuplicateIDs(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "usage.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	entry := usage.Entry{
		ID:         "event-a",
		Provider:   usage.ProviderCodex,
		SourceFile: "/tmp/session.jsonl",
		SourceLine: 1,
		Timestamp:  time.Date(2026, 6, 4, 1, 2, 3, 0, time.UTC),
		Date:       "2026-06-04",
		Project:    "tracklm",
		Usage: usage.TokenUsage{
			InputTokens:  1,
			OutputTokens: 2,
			TotalTokens:  3,
		},
	}

	inserted, err := db.InsertEvents([]usage.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 {
		t.Fatalf("inserted = %d, want 1", inserted)
	}

	inserted, err = db.InsertEvents([]usage.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 {
		t.Fatalf("duplicate inserted = %d, want 0", inserted)
	}

	summaries, err := db.DailyProjectSummaries("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].TotalTokens != 3 {
		t.Fatalf("total tokens = %d, want 3", summaries[0].TotalTokens)
	}
}
