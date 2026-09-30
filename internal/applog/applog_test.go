package applog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

func readLog(t *testing.T, dir string) []map[string]any {
	t.Helper()
	file, err := os.Open(store.LogPath(dir, store.LogFile))
	if errors.Is(err, os.ErrNotExist) {
		// Nothing logged yet: the file is created by its first line.
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var lines []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", scanner.Text())
		}
		lines = append(lines, line)
	}
	return lines
}

// The file is for reading after the fact, so it keeps everything; stderr is
// read by the front-ends that launch the CLI, so it keeps what it always had.
func TestFileTakesEveryLevelAndStderrStaysAsItWas(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	logger := New(dir, &stderr, slog.String("cmd", "sync"), slog.Int("pid", 42))

	logger.Debug("skip upload; API key is not configured")
	logger.Info("scan finished", "inserted", 3)
	logger.Warn("tokitoki failed", "error", errors.New("dial tcp: no route to host"))
	logger.Error("tokitoki failed", "error", errors.New("open usage db: disk I/O error"))

	lines := readLog(t, dir)
	if len(lines) != 4 {
		t.Fatalf("file has %d lines, want all 4 levels", len(lines))
	}
	for i, want := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		if lines[i]["level"] != want {
			t.Errorf("line %d level = %v, want %s", i, lines[i]["level"], want)
		}
		if lines[i]["cmd"] != "sync" || lines[i]["pid"] != float64(42) {
			t.Errorf("line %d is missing the run's attrs: %v", i, lines[i])
		}
	}
	if lines[3]["error"] != "open usage db: disk I/O error" {
		t.Errorf("error text not recorded: %v", lines[3])
	}

	out := stderr.String()
	if strings.Contains(out, "scan finished") || strings.Contains(out, "skip upload") {
		t.Errorf("stderr gained lines below Warn:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("stderr lost its warnings or errors:\n%s", out)
	}
	if strings.Contains(out, "pid=") {
		t.Errorf("run attrs leaked onto stderr, which front-ends read:\n%s", out)
	}
}

func TestLogFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	New(dir, &bytes.Buffer{}).Info("hello")

	info, err := os.Stat(store.LogPath(dir, store.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("log file mode %v is readable by others", perm)
	}
}

// Logging is never a reason for a command to fail.
func TestUnusableLogDirectoryFallsBackToStderr(t *testing.T) {
	dir := t.TempDir()
	// A file where the log directory belongs.
	if err := os.WriteFile(store.LogPath(dir, ""), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	New(dir, &stderr).Error("still heard")
	if !strings.Contains(stderr.String(), "still heard") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	New("", &stderr).Warn("no data dir at all")
	if !strings.Contains(stderr.String(), "no data dir at all") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRotatesInsteadOfGrowingForever(t *testing.T) {
	dir := t.TempDir()
	logger := New(dir, &bytes.Buffer{})
	chunk := strings.Repeat("x", 64<<10)
	for written := 0; written < (maxSizeMB+1)<<20; written += len(chunk) {
		logger.Info(chunk)
	}

	entries, err := os.ReadDir(store.LogPath(dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("wrote past the limit and still have %d file(s)", len(entries))
	}
	info, err := os.Stat(store.LogPath(dir, store.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxSizeMB<<20 {
		t.Errorf("live log is %d bytes, over the %dMB limit", info.Size(), maxSizeMB)
	}
}

// A crash cannot log itself; the run after it does.
func TestPreviousCrashBecomesOneErrorLine(t *testing.T) {
	dir := t.TempDir()
	logger := New(dir, &bytes.Buffer{})
	CaptureCrashes(dir, logger)
	if lines := readLog(t, dir); len(lines) != 0 {
		t.Fatalf("no crash on record, yet logged: %v", lines)
	}

	trace := "panic: runtime error: index out of range\n\ngoroutine 7 [running]:\nmain.scan()\n"
	if err := os.WriteFile(store.LogPath(dir, store.CrashFile), []byte(trace), 0o600); err != nil {
		t.Fatal(err)
	}
	CaptureCrashes(dir, logger)

	lines := readLog(t, dir)
	if len(lines) != 1 || lines[0]["level"] != "ERROR" || lines[0]["trace"] != trace {
		t.Fatalf("want one ERROR line carrying the trace, got %v", lines)
	}

	// Reported once, not on every run after.
	CaptureCrashes(dir, logger)
	if lines := readLog(t, dir); len(lines) != 1 {
		t.Fatalf("crash reported again: %d lines", len(lines))
	}
}

// The mechanism itself, for real: a child process panics in a goroutine —
// where no recover() in main could reach it — and dies. Its trace has to be
// in the crash file, and the next run has to turn it into a log line.
func TestAGoroutinePanicIsCapturedAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("APPLOG_CRASH_CHILD"); dir != "" {
		CaptureCrashes(dir, New(dir, os.Stderr))
		done := make(chan struct{})
		go func() {
			defer close(done)
			var events []int
			_ = events[3] // index out of range
		}()
		<-done
		return
	}

	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestAGoroutinePanicIsCapturedAcrossProcesses$")
	child.Env = append(os.Environ(), "APPLOG_CRASH_CHILD="+dir)
	if err := child.Run(); err == nil {
		t.Fatal("the child was supposed to crash")
	}

	CaptureCrashes(dir, New(dir, &bytes.Buffer{}))
	lines := readLog(t, dir)
	if len(lines) != 1 || lines[0]["msg"] != "previous run crashed" {
		t.Fatalf("want the crash as one log line, got %v", lines)
	}
	trace, _ := lines[0]["trace"].(string)
	for _, want := range []string{"index out of range", "goroutine", "applog_test.go"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace is missing %q:\n%s", want, trace)
		}
	}
}
