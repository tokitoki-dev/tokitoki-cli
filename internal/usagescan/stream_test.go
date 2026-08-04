package usagescan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/claude"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/kimi"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/pi"
	"github.com/tokitoki-dev/tokitoki-cli/internal/provider/qwen"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageprovider"
)

func claudeUsageLine(id string) string {
	return `{"timestamp":"2026-06-04T01:02:03Z","cwd":"/Users/me/workspace/tokitoki","requestId":"req-` + id +
		`","message":{"id":"msg-` + id + `","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
}

func newClaudeScan(t *testing.T) (*Scanner, map[usage.Provider][]string, string) {
	t.Helper()
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, "claude")
	sessionDir := filepath.Join(claudeDir, "projects", "-Users-me-workspace-tokitoki")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "session-a.jsonl")
	if err := os.WriteFile(sessionFile, []byte(claudeUsageLine("1")), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return New(db, claude.Provider{}), map[usage.Provider][]string{usage.ProviderClaude: {claudeDir}}, sessionFile
}

// An append must cost only the appended record. The whole point of storing a
// resume offset is that a transcript that has grown by one line is not read
// from the beginning again.
func TestScanStreamingParsesOnlyAppendedEntries(t *testing.T) {
	scanner, dirs, sessionFile := newClaudeScan(t)

	result, err := scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if parsed := result.Providers[usage.ProviderClaude].EventsParsed; parsed != 1 {
		t.Fatalf("first events parsed = %d, want 1", parsed)
	}

	result, err = scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if parsed := result.Providers[usage.ProviderClaude].EventsParsed; parsed != 0 {
		t.Fatalf("unchanged file events parsed = %d, want 0", parsed)
	}

	appendLine(t, sessionFile, claudeUsageLine("2"))

	result, err = scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	claudeResult := result.Providers[usage.ProviderClaude]
	if claudeResult.EventsParsed != 1 {
		t.Fatalf("appended file events parsed = %d, want 1 (only the new line)", claudeResult.EventsParsed)
	}
	if claudeResult.EventsInserted != 1 {
		t.Fatalf("appended file events inserted = %d, want 1", claudeResult.EventsInserted)
	}
}

// A transcript that shrank was truncated or replaced, so the stored offset
// points at content that no longer exists and the file must be re-read whole.
func TestScanStreamingRescansTruncatedFile(t *testing.T) {
	scanner, dirs, sessionFile := newClaudeScan(t)

	if _, err := scanner.Scan(dirs); err != nil {
		t.Fatal(err)
	}
	appendLine(t, sessionFile, claudeUsageLine("2"))
	if _, err := scanner.Scan(dirs); err != nil {
		t.Fatal(err)
	}

	// Replace the file with a shorter one holding a different record.
	if err := os.WriteFile(sessionFile, []byte(claudeUsageLine("3")), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	claudeResult := result.Providers[usage.ProviderClaude]
	if claudeResult.EventsParsed != 1 {
		t.Fatalf("truncated file events parsed = %d, want 1 (re-read from the start)", claudeResult.EventsParsed)
	}
	if claudeResult.EventsInserted != 1 {
		t.Fatalf("truncated file events inserted = %d, want 1", claudeResult.EventsInserted)
	}
}

// Progress is recorded per file, so a scan that fails partway leaves the
// files it already stored marked as done.
func TestScanStreamingRecordsOffsetPerFile(t *testing.T) {
	scanner, dirs, sessionFile := newClaudeScan(t)

	if _, err := scanner.Scan(dirs); err != nil {
		t.Fatal(err)
	}

	db := scanner.db
	states, err := db.ScannedFiles()
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states[sessionFile]
	if !ok {
		t.Fatalf("no scanned state recorded for %s", sessionFile)
	}
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if state.Offset != info.Size() {
		t.Fatalf("offset = %d, want %d", state.Offset, info.Size())
	}
	if state.Size != info.Size() {
		t.Fatalf("size = %d, want %d", state.Size, info.Size())
	}
}

func appendLine(t *testing.T, path, data string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

// Every provider that opts into streaming must satisfy the same contract: a
// full first pass, no work when nothing changed, and only the appended record
// on the pass after an append.
func TestScanStreamingProvidersReadOnlyAppendedRecords(t *testing.T) {
	tests := []struct {
		name     string
		provider usageprovider.Provider
		id       usage.Provider
		relPath  string
		line     func(n string) string
	}{
		{
			name:     "claude",
			provider: claude.Provider{},
			id:       usage.ProviderClaude,
			relPath:  filepath.Join("projects", "-Users-me-workspace-tokitoki", "session-a.jsonl"),
			line:     func(n string) string { return claudeUsageLine(n) },
		},
		{
			name:     "pi",
			provider: pi.Provider{},
			id:       usage.ProviderPi,
			relPath:  "sess_abc.jsonl",
			line: func(n string) string {
				return `{"type":"message","timestamp":"2026-06-04T01:02:0` + n + `Z","message":{"role":"assistant","model":"m","usage":{"input":1,"output":` + n + `}}}` + "\n"
			},
		},
		{
			name:     "qwen",
			provider: qwen.Provider{},
			id:       usage.ProviderQwen,
			relPath:  filepath.Join("projects", "proj", "chats", "chat-a.jsonl"),
			line: func(n string) string {
				return `{"type":"assistant","timestamp":"2026-06-04T01:02:0` + n + `Z","model":"m","usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":` + n + `}}` + "\n"
			},
		},
		{
			name:     "kimi",
			provider: kimi.Provider{},
			id:       usage.ProviderKimi,
			relPath:  filepath.Join("sessions", "ws", "sess", "wire.jsonl"),
			line: func(n string) string {
				return `{"type":"usage.record","usageScope":"turn","time":"2026-06-04T01:02:0` + n + `Z","model":"m","usage":{"inputOther":1,"output":` + n + `}}` + "\n"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "root")
			file := filepath.Join(root, tt.relPath)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(tt.line("1")), 0o600); err != nil {
				t.Fatal(err)
			}

			db, err := usagedb.Open(filepath.Join(dir, "usage.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })

			scanner := New(db, tt.provider)
			dirs := map[usage.Provider][]string{tt.id: {root}}

			first, err := scanner.Scan(dirs)
			if err != nil {
				t.Fatal(err)
			}
			if parsed := first.Providers[tt.id].EventsParsed; parsed != 1 {
				t.Fatalf("first pass parsed = %d, want 1", parsed)
			}

			second, err := scanner.Scan(dirs)
			if err != nil {
				t.Fatal(err)
			}
			if parsed := second.Providers[tt.id].EventsParsed; parsed != 0 {
				t.Fatalf("unchanged pass parsed = %d, want 0", parsed)
			}

			appendLine(t, file, tt.line("2"))

			third, err := scanner.Scan(dirs)
			if err != nil {
				t.Fatal(err)
			}
			result := third.Providers[tt.id]
			if result.EventsParsed != 1 {
				t.Fatalf("appended pass parsed = %d, want 1 (only the new record)", result.EventsParsed)
			}
			if result.EventsInserted != 1 {
				t.Fatalf("appended pass inserted = %d, want 1", result.EventsInserted)
			}
		})
	}
}

// A file appended to while it is being parsed must not be skipped on the next
// scan. The recorded size describes the file as the parser found it, so the
// appended bytes make the file look changed and get read next time.
func TestScanStreamingDoesNotLoseDataAppendedDuringTheScan(t *testing.T) {
	scanner, dirs, sessionFile := newClaudeScan(t)
	if _, err := scanner.Scan(dirs); err != nil {
		t.Fatal(err)
	}

	// Reproduce the state an append during the parse leaves behind: the file
	// now holds a second record, but the scan only consumed the first.
	appendLine(t, sessionFile, claudeUsageLine("2"))
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	consumed := int64(len(claudeUsageLine("1")))
	if err := scanner.db.UpsertScannedFiles(map[string]usagedb.FileState{
		sessionFile: {Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), Offset: consumed},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := scanner.Scan(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if parsed := result.Providers[usage.ProviderClaude].EventsParsed; parsed != 1 {
		t.Fatalf("parsed = %d, want 1: the appended record was skipped and is lost", parsed)
	}
}
