package logship

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

// sink is a stand-in for POST /api/diagnostics that keeps what it was sent.
type sink struct {
	*httptest.Server
	mu       sync.Mutex
	status   int
	requests []*http.Request
	reports  []payload
}

func newSink(t *testing.T) *sink {
	t.Helper()
	s := &sink{status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			unzipped, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("body is not gzip: %v", err)
				return
			}
			body = unzipped
		}
		var report payload
		if err := json.NewDecoder(body).Decode(&report); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		s.requests = append(s.requests, r)
		s.reports = append(s.reports, report)
		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sink) events() []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []event
	for _, report := range s.reports {
		all = append(all, report.Events...)
	}
	return all
}

func (s *sink) posts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// logLine is one line as applog writes it.
func logLine(when time.Time, level string, pid int, cmd, msg string, attrs ...any) string {
	line := map[string]any{
		"time": when.Format(time.RFC3339Nano), "level": level, "msg": msg,
		"cmd": cmd, "version": "0.1.10", "pid": pid,
	}
	for i := 0; i+1 < len(attrs); i += 2 {
		line[attrs[i].(string)] = attrs[i+1]
	}
	data, _ := json.Marshal(line)
	return string(data) + "\n"
}

func appendLog(t *testing.T, dir string, lines ...string) {
	t.Helper()
	path := store.LogPath(dir, store.LogFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

// ship runs one pass with the interval out of the way; the interval has its
// own test.
func ship(t *testing.T, dir string, s *sink, key string) error {
	t.Helper()
	return Ship(context.Background(), Options{DataDir: dir, BaseURL: s.URL, APIKey: key, Force: true})
}

func TestForwardsErrorLinesWithWhatLedUpToThem(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir,
		logLine(now, "INFO", 7, "sync", "run started"),
		logLine(now, "INFO", 8, "heartbeat", "run started"),
		logLine(now, "DEBUG", 7, "sync", "scan finished", "parsed", 73),
		logLine(now, "WARN", 7, "sync", "tokitoki failed", "error", "dial tcp: no route to host"),
		logLine(now, "ERROR", 7, "sync", "provider scan failed", "provider", "goose", "error", "database disk image is malformed"),
		logLine(now, "INFO", 7, "sync", "run finished", "exit_code", 1),
		logLine(now, "INFO", 8, "heartbeat", "run finished", "exit_code", 0),
	)

	if err := ship(t, dir, s, "tokitoki_key"); err != nil {
		t.Fatal(err)
	}

	events := s.events()
	if len(events) != 1 {
		t.Fatalf("forwarded %d events, want only the ERROR line: %+v", len(events), events)
	}
	e := events[0]
	if e.Msg != "provider scan failed" || e.Cmd != "sync" || e.PID != 7 || e.Count != 1 {
		t.Errorf("event = %+v", e)
	}
	if e.Attrs["provider"] != "goose" || e.Attrs["error"] != "database disk image is malformed" || e.Attrs["version"] != "0.1.10" {
		t.Errorf("attrs = %v", e.Attrs)
	}
	// The lead-up is this process's lines only — not the heartbeat that
	// happened to be running beside it — and stops at the error.
	if len(e.Context) != 3 {
		t.Fatalf("context has %d lines, want pid 7's three before the error: %v", len(e.Context), e.Context)
	}
	for _, line := range e.Context {
		if !strings.Contains(line, `"pid":7`) {
			t.Errorf("context leaked another process's line: %s", line)
		}
	}

	r := s.requests[0]
	if r.URL.Path != "/api/diagnostics" || r.Header.Get("Content-Encoding") != "gzip" ||
		r.Header.Get("Authorization") != "Bearer tokitoki_key" || !strings.HasPrefix(r.Header.Get("User-Agent"), "tokitoki-cli/") {
		t.Errorf("request = %s %v", r.URL.Path, r.Header)
	}
	if s.reports[0].MachineID == "" && s.reports[0].Platform == "" {
		t.Errorf("report does not identify the install: %+v", s.reports[0])
	}

	// Delivered is delivered: the next pass has nothing to say and says nothing.
	if err := ship(t, dir, s, "tokitoki_key"); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 1 {
		t.Fatalf("second pass posted again (%d posts)", s.posts())
	}
}

// Errors logged while the server is unreachable are still in the file when it
// comes back. This is what reading the log buys over reporting from inside
// the failing code.
func TestUndeliveredErrorsAreSentWhenTheServerReturns(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir, logLine(now, "ERROR", 7, "sync", "tokitoki failed", "error", "open usage db: disk I/O error"))

	s.status = http.StatusServiceUnavailable
	if err := ship(t, dir, s, ""); err == nil {
		t.Fatal("a refused report must be an error to the caller")
	}
	s.status = http.StatusOK
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 2 {
		t.Fatalf("posts = %d, want the retry", s.posts())
	}
	if last := s.reports[1].Events; len(last) != 1 || last[0].Msg != "tokitoki failed" {
		t.Fatalf("retry carried %+v, want the same error again", last)
	}
	// No key is no obstacle: the report goes out unsigned.
	if got := s.requests[1].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q with no key configured", got)
	}
}

func TestOnePassInTenMinutesUnlessForced(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir, logLine(now, "ERROR", 7, "sync", "first"))
	opts := Options{DataDir: dir, BaseURL: s.URL}

	if err := Ship(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, logLine(now, "ERROR", 8, "sync", "second"))
	if err := Ship(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 1 {
		t.Fatalf("posts = %d: a second pass ran inside the interval", s.posts())
	}

	// A crash is forwarded at the start of the next run whatever the clock says.
	opts.Force = true
	if err := Ship(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 2 {
		t.Fatalf("posts = %d: Force did not skip the interval", s.posts())
	}
}

// A broken install fails the same way on every sync. It is reported once a
// day, and the report says how many times it happened.
func TestARepeatingFaultIsReportedOnceADayWithItsCount(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	fault := func(pid int) string {
		return logLine(now, "ERROR", pid, "sync", "provider scan failed", "provider", "goose",
			"error", fmt.Sprintf("read session %d: permission denied", pid))
	}

	appendLog(t, dir, fault(1), fault(2), fault(3))
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if events := s.events(); len(events) != 1 || events[0].Count != 3 {
		t.Fatalf("first pass = %+v, want one event counting 3", events)
	}

	appendLog(t, dir, fault(4), fault(5))
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 1 {
		t.Fatalf("the same fault was reported twice in a day (%d posts)", s.posts())
	}

	// A day later it still happens: report it, and the two that were held back.
	var sent map[string]shipped
	readJSON(store.StatePath(dir, shippedFile), &sent)
	for fp, record := range sent {
		record.At = record.At.Add(-25 * time.Hour)
		sent[fp] = record
	}
	if err := writeJSON(store.StatePath(dir, shippedFile), sent); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, fault(6))
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	events := s.events()
	if len(events) != 2 || events[1].Count != 3 {
		t.Fatalf("next day's report = %+v, want count 3 (one new + two held back)", events)
	}
}

// Rotation swaps the file under the same name. The old offset means nothing
// in the new file — and after a long time offline the new file may already be
// longer than it, so size cannot be what notices.
func TestRotationStartsTheNewFileFromItsBeginning(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir,
		logLine(now, "INFO", 1, "sync", strings.Repeat("padding ", 40)),
		logLine(now, "ERROR", 1, "sync", "in the old file"),
	)
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(store.LogPath(dir, store.LogFile)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	appendLog(t, dir,
		logLine(later, "ERROR", 2, "sync", "first line of the new file"),
		logLine(later, "INFO", 2, "sync", strings.Repeat("padding ", 200)),
	)
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}

	events := s.events()
	if len(events) != 2 || events[1].Msg != "first line of the new file" {
		t.Fatalf("events = %+v, want the new file read from its first line", events)
	}
}

// A run that was killed — a front-end's 140-second timeout, the OOM killer —
// logged its start and nothing after. No line of its own could report that;
// the missing finish line does.
func TestARunThatNeverFinishedIsReported(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir,
		logLine(now.Add(-time.Hour), "INFO", 41, "sync", "run started"),
		logLine(now.Add(-time.Hour), "INFO", 42, "sync", "run started"),
		logLine(now.Add(-time.Hour), "INFO", 42, "sync", "run finished", "exit_code", 0),
		// The service starts once and is not expected to finish.
		logLine(now.Add(-time.Hour), "INFO", 43, "__service-run", "run started"),
		// Still within its time: might be running right now.
		logLine(now.Add(-time.Minute), "INFO", 44, "heartbeat", "run started"),
	)
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}

	events := s.events()
	if len(events) != 1 || events[0].Msg != "run did not finish" || events[0].PID != 41 || events[0].Cmd != "sync" {
		t.Fatalf("events = %+v, want only pid 41 reported", events)
	}

	// pid 44's start line is behind the cursor now. It has to be remembered,
	// or a run that dies young is never noticed.
	var cursor state
	readJSON(store.StatePath(dir, cursorFile), &cursor)
	if _, waiting := cursor.Pending["44"]; !waiting || len(cursor.Pending) != 1 {
		t.Fatalf("pending = %v, want pid 44 alone", cursor.Pending)
	}

	// The OS does not hand out a live pid twice: 44 starting again means the
	// first 44 is gone, however recently it began.
	appendLog(t, dir, logLine(now, "INFO", 44, "sync", "run started"), logLine(now, "INFO", 44, "sync", "run finished"))
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	events = s.events()
	if len(events) != 2 || events[1].PID != 44 || events[1].Cmd != "heartbeat" {
		t.Fatalf("events = %+v, want the first pid 44 reported as unfinished", events)
	}
}

func TestTheHomeDirectoryNeverLeavesTheMachine(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Users", "zoe-quimby")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir, s, now := t.TempDir(), newSink(t), time.Now()

	db := filepath.Join(home, ".tokitoki", "data", "tokitoki.db")
	appendLog(t, dir,
		logLine(now, "DEBUG", 7, "sync", "scanning", "dir", filepath.Join(home, ".claude")),
		logLine(now, "ERROR", 7, "sync", "tokitoki failed", "error", "open "+db+": permission denied",
			"nested", map[string]any{"paths": []any{filepath.Join(home, "work")}}),
	)
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(s.reports[0])
	if strings.Contains(string(raw), home) || strings.Contains(string(raw), "zoe-quimby") {
		t.Fatalf("the report names the home directory:\n%s", raw)
	}
	if got := s.events()[0].Attrs["error"]; got != "open ~/.tokitoki/data/tokitoki.db: permission denied" {
		t.Errorf("error = %q", got)
	}
}

// Context lines are raw JSON, where a Windows path has its backslashes doubled.
func TestScrubFindsAWindowsHomeInsideRawJSON(t *testing.T) {
	events := []event{{
		Msg:     "tokitoki failed",
		Attrs:   map[string]any{"error": `open C:\Users\ann\.tokitoki\data\tokitoki.db: Access is denied.`},
		Context: []string{`{"msg":"scanning","dir":"C:\\Users\\ann\\.claude"}`},
	}}
	scrub(events, `C:\Users\ann`)
	if got := events[0].Attrs["error"]; got != `open ~\.tokitoki\data\tokitoki.db: Access is denied.` {
		t.Errorf("error = %q", got)
	}
	if got := events[0].Context[0]; got != `{"msg":"scanning","dir":"~\\.claude"}` {
		t.Errorf("context = %q", got)
	}
}

// Uploading switched off means nothing leaves the machine.
func TestNothingIsSentWhileUploadingIsSwitchedOff(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	appendLog(t, dir, logLine(now, "ERROR", 7, "sync", "tokitoki failed"))
	if err := store.DisableUpload(dir); err != nil {
		t.Fatal(err)
	}
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 0 {
		t.Fatalf("posted %d times with uploading disabled", s.posts())
	}

	// Switched back on, what was logged meanwhile goes out.
	if err := store.EnableUpload(dir); err != nil {
		t.Fatal(err)
	}
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if len(s.events()) != 1 {
		t.Fatalf("events = %+v", s.events())
	}
}

// Another process may be halfway through writing the last line. It is left
// alone and read whole next time, not skipped and not sent as garbage.
func TestALineStillBeingWrittenWaitsForTheNextPass(t *testing.T) {
	dir, s, now := t.TempDir(), newSink(t), time.Now()
	whole := logLine(now, "ERROR", 7, "sync", "written in two halves", "error", "disk I/O error")
	appendLog(t, dir, logLine(now, "INFO", 7, "sync", "run started"), whole[:len(whole)/2])
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 0 {
		t.Fatalf("half a line was forwarded: %+v", s.events())
	}

	appendLog(t, dir, whole[len(whole)/2:])
	if err := ship(t, dir, s, ""); err != nil {
		t.Fatal(err)
	}
	if events := s.events(); len(events) != 1 || events[0].Msg != "written in two halves" {
		t.Fatalf("events = %+v", events)
	}
}

func TestNoLogIsNotAnError(t *testing.T) {
	s := newSink(t)
	if err := ship(t, t.TempDir(), s, ""); err != nil {
		t.Fatal(err)
	}
	if s.posts() != 0 {
		t.Fatal("posted with no log to read")
	}
}
