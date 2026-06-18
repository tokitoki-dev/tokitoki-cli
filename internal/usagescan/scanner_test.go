package usagescan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/labx/tokitoki-agent/internal/usagedb"
)

func TestScanAllSkipsUnchangedFiles(t *testing.T) {
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
	t.Setenv("CODEX_CONFIG_DIR", codexDir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "missing-claude"))

	db, err := usagedb.Open(filepath.Join(dir, "usage.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	scanner := New(db)
	result, err := scanner.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if result.Codex.FilesScanned != 1 {
		t.Fatalf("first files scanned = %d, want 1", result.Codex.FilesScanned)
	}
	if result.Codex.EventsInserted != 1 {
		t.Fatalf("first events inserted = %d, want 1", result.Codex.EventsInserted)
	}

	result, err = scanner.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if result.Codex.FilesSkipped != 1 {
		t.Fatalf("second files skipped = %d, want 1", result.Codex.FilesSkipped)
	}
	if result.Codex.EventsInserted != 0 {
		t.Fatalf("second events inserted = %d, want 0", result.Codex.EventsInserted)
	}
}
