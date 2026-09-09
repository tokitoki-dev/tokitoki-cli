package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func usageLine(id string, input, output uint64) string {
	return `{"timestamp":"2026-05-21T01:02:03Z","cwd":"/tmp/p","requestId":"req-` + id +
		`","message":{"id":"msg-` + id + `","model":"claude","usage":{"input_tokens":` +
		itoa(input) + `,"output_tokens":` + itoa(output) + `}}}` + "\n"
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

func transcript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects", "-tmp-p", "session-a.jsonl")
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, body)
	return path
}

func appendTo(t *testing.T, path, data string) {
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

// Resuming at the reported offset must produce exactly what a single pass
// produces. This is the property the incremental scan rests on.
func TestReadUsageFileFromResumeMatchesWholeRead(t *testing.T) {
	path := transcript(t, usageLine("1", 1, 2)+usageLine("2", 3, 4)+usageLine("3", 5, 6))

	whole, wholeOffset, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 3 {
		t.Fatalf("whole read = %d entries, want 3", len(whole))
	}

	first, firstOffset, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Resume from after the first entry, as an interrupted pass would.
	rest, restOffset, err := ReadUsageFileFrom(path, first[0].SourceEnd)
	if err != nil {
		t.Fatal(err)
	}
	if restOffset != wholeOffset || firstOffset != wholeOffset {
		t.Fatalf("offsets = %d/%d, want %d", firstOffset, restOffset, wholeOffset)
	}
	if len(rest) != 2 {
		t.Fatalf("resumed read = %d entries, want 2", len(rest))
	}
	for i, entry := range rest {
		if entry.ID != whole[i+1].ID {
			t.Fatalf("resumed entry %d id = %q, want %q", i, entry.ID, whole[i+1].ID)
		}
	}
}

// Appending to a transcript must cost only the appended bytes.
func TestReadUsageFileFromReadsOnlyTheAppendedTail(t *testing.T) {
	path := transcript(t, usageLine("1", 1, 2))
	_, offset, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	appendTo(t, path, usageLine("2", 3, 4))
	tail, newOffset, err := ReadUsageFileFrom(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 {
		t.Fatalf("tail read = %d entries, want 1", len(tail))
	}
	if tail[0].Data.Message.Usage.InputTokens != 3 {
		t.Fatalf("tail input tokens = %d, want 3", tail[0].Data.Message.Usage.InputTokens)
	}
	if newOffset <= offset {
		t.Fatalf("offset did not advance: %d -> %d", offset, newOffset)
	}
}

// A line still being written has no newline yet. It is parsed so a file that
// merely lacks a trailing newline is not ignored, but the resume offset stops
// before it so the completed line is read again.
func TestReadUsageFileFromDoesNotConsumePartialLine(t *testing.T) {
	complete := usageLine("1", 1, 2)
	path := transcript(t, complete+`{"timestamp":"2026-05-21T01:02:03Z","cwd":"/tmp/p","mess`)

	entries, offset, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if offset != int64(len(complete)) {
		t.Fatalf("offset = %d, want %d (partial line must not be consumed)", offset, len(complete))
	}

	// The rest of the line arrives; the next pass sees the whole record.
	appendTo(t, path, `age":{"id":"msg-2","model":"claude","usage":{"input_tokens":9,"output_tokens":9}}}`+"\n")
	rest, _, err := ReadUsageFileFrom(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Data.Message.Usage.InputTokens != 9 {
		t.Fatalf("completed line not recovered: %+v", rest)
	}
}

// A file with no trailing newline at all is a finished file, not a partial
// write, and its last line must still be ingested.
func TestReadUsageFileFromParsesFinalLineWithoutNewline(t *testing.T) {
	path := transcript(t, usageLine("1", 1, 2)+`{"timestamp":"2026-05-21T01:02:03Z","cwd":"/tmp/p","requestId":"req-2","message":{"id":"msg-2","model":"claude","usage":{"input_tokens":8,"output_tokens":8}}}`)

	entries, _, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (final line without newline must parse)", len(entries))
	}
}

// A line that cannot be parsed must not become a permanent roadblock: the
// offset advances past it so later records stay reachable.
func TestReadUsageFileFromAdvancesPastUnparsableLine(t *testing.T) {
	path := transcript(t, "{not json}\n"+usageLine("2", 4, 5))

	entries, offset, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Fatalf("offset = %d, want %d (bad line must not stall the scan)", offset, info.Size())
	}
}

// Because every entry comes from exactly one line, resuming at any line
// boundary yields the entries a whole read yields for the lines after it —
// including an edit whose issuing message sits before the boundary.
func TestReadUsageFileFromAnyLineBoundaryMatchesWholeRead(t *testing.T) {
	lines := []string{
		usageLine("1", 1, 2),
		`{"timestamp":"2026-05-21T01:02:03Z","cwd":"/tmp/p","requestId":"req-1","message":{"id":"msg-1","model":"claude","usage":{"input_tokens":1,"output_tokens":2},"content":[{"type":"tool_use","id":"toolu-1","name":"Edit","input":{}}]}}` + "\n",
		`{"type":"user","timestamp":"2026-05-21T01:02:04Z","cwd":"/tmp/p","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-1"}]},"toolUseResult":{"filePath":"/tmp/p/a.go","structuredPatch":[{"lines":["+x","+y"]}]}}` + "\n",
		usageLine("2", 3, 4),
	}
	body := ""
	for _, line := range lines {
		body += line
	}
	path := transcript(t, body)

	whole, _, err := ReadUsageFileFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 4 {
		t.Fatalf("whole read = %d entries, want 4", len(whole))
	}

	offset := int64(0)
	for i, line := range lines {
		rest, _, err := ReadUsageFileFrom(path, offset)
		if err != nil {
			t.Fatal(err)
		}
		want := whole[i:]
		if len(rest) != len(want) {
			t.Fatalf("resume at line %d: %d entries, want %d", i, len(rest), len(want))
		}
		for j := range rest {
			if rest[j].ID != want[j].ID || rest[j].LinesAdded != want[j].LinesAdded || rest[j].Kind != want[j].Kind {
				t.Fatalf("resume at line %d entry %d = %+v, want %+v", i, j, rest[j], want[j])
			}
		}
		offset += int64(len(line))
	}
}
