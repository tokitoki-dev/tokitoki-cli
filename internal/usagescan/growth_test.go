package usagescan

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/hermes"
	"github.com/tokitoki-dev/tokitoki-cli/internal/providertest"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
)

// A Hermes session keeps one row whose token counts grow while it runs. Read
// mid-session and again after it grew, it must count once: the events of the
// session add up to its final total, not to every total ever read.
func TestScanCountsAGrowingSessionOnce(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "hermes", "state.db")
	source := providertest.OpenTestSQLite(t, state)
	providertest.ExecSQL(t, source, `CREATE TABLE sessions (id TEXT PRIMARY KEY, model TEXT, billing_provider TEXT,
		started_at REAL, message_count INTEGER, input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
		cache_write_tokens INTEGER, reasoning_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL)`)
	providertest.ExecSQL(t, source, `INSERT INTO sessions VALUES ('s1', 'claude-sonnet-4', 'anthropic', 1780000000, 3, 1000, 200, 0, 0, 0, 0, 0)`)

	db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := New(db, hermes.Provider{})
	scan := func() {
		t.Helper()
		if _, err := scanner.Scan(map[usage.Provider][]string{usage.ProviderHermes: {filepath.Dir(state)}}); err != nil {
			t.Fatal(err)
		}
	}

	scan()
	scan() // nothing changed
	providertest.ExecSQL(t, source, `UPDATE sessions SET message_count = 6, input_tokens = 2500, output_tokens = 500 WHERE id = 's1'`)
	scan()
	_ = source.Close()

	events, err := db.PendingEvents(time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	var tokens uint64
	for _, event := range events {
		tokens += event.Usage.TotalTokens
	}
	if len(events) != 2 || tokens != 3000 {
		t.Fatalf("%d events, %d tokens; want 2 events summing to the session's 3000", len(events), tokens)
	}
}
