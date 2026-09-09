package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
	"github.com/tokitoki-dev/tokitoki-cli/pkg/agentlib"
)

func TestRunUploadSwitchTogglesSentinel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, config.DataDirName)
	sentinel := filepath.Join(dataDir, "state", store.DisabledFile)

	if code := run([]string{"upload", "disable"}); code != 0 {
		t.Fatalf("run(upload disable) = %d, want 0", code)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("stat sentinel after disable: %v", err)
	}

	if code := run([]string{"upload", "enable"}); code != 0 {
		t.Fatalf("run(upload enable) = %d, want 0", code)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("stat sentinel after enable: err = %v, want not-exist", err)
	}
}

// status reports without changing anything, so front-ends can poll it.
func TestRunUploadStatusLeavesSwitchAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sentinel := filepath.Join(home, config.DataDirName, "state", store.DisabledFile)

	if code := run([]string{"upload", "status"}); code != 0 {
		t.Fatalf("run(upload status) = %d, want 0", code)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("upload status created the sentinel, want no change")
	}

	if code := run([]string{"upload", "disable"}); code != 0 {
		t.Fatalf("run(upload disable) = %d, want 0", code)
	}
	if code := run([]string{"upload", "status"}); code != 0 {
		t.Fatalf("run(upload status) = %d, want 0", code)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("upload status removed the sentinel: %v", err)
	}
}

func TestRunUploadSwitchRejectsBadSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"upload"},
		{"upload", "on"},
		{"upload", "enable", "extra"},
	} {
		if code := run(args); code != 2 {
			t.Fatalf("run(%v) = %d, want 2", args, code)
		}
	}
}

// writeClaudeTranscript plants one scannable usage record so a sync has
// something real to queue and, when enabled, to upload.
func writeClaudeTranscript(t *testing.T, home string) string {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	sessionDir := filepath.Join(claudeDir, "projects", "-Users-me-workspace-tokitoki")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-06-04T01:02:03Z","cwd":"/Users/me/workspace/tokitoki",` +
		`"requestId":"req-1","message":{"id":"msg-1","model":"claude",` +
		`"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "session-a.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return claudeDir
}

// countingUploadServer accepts every batch and counts the requests, so a test
// can tell "nothing was sent" apart from "sending failed".
func countingUploadServer(t *testing.T, requests *atomic.Int64) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted := []string{}
		if r.URL.Path == "/api/usage-events/batch" {
			requests.Add(1)
			// Acknowledge every event by id: an upload the server only
			// half-answers is reported as incomplete, which would make this
			// helper look like a failure of the code under test.
			body := io.Reader(r.Body)
			if r.Header.Get("Content-Encoding") == "gzip" {
				gr, err := gzip.NewReader(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				defer gr.Close()
				body = gr
			}
			var payload usageupload.Payload
			if err := json.NewDecoder(body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			for _, event := range payload.Events {
				accepted = append(accepted, event.ID)
			}
		}
		_ = json.NewEncoder(w).Encode(usageupload.Response{
			OK:        true,
			Accepted:  accepted,
			Duplicate: []string{},
			Rejected:  []usageupload.Reject{},
		})
	}))
	t.Cleanup(server.Close)
	previousServer := config.ServerURL
	config.ServerURL = server.URL
	t.Cleanup(func() { config.ServerURL = previousServer })
}

func pendingEvents(t *testing.T, home string) int {
	t.Helper()
	db, err := usagedb.Open(store.UsageDBPath(filepath.Join(home, config.DataDirName)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count, err := db.PendingCount(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// The point of the switch: a disabled sync sends nothing but keeps scanning,
// so the events it found are queued rather than dropped.
func TestSyncWithUploadDisabledScansButDoesNotUpload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeTranscript(t, home)
	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	var requests atomic.Int64
	countingUploadServer(t, &requests)

	if code := run([]string{"upload", "disable"}); code != 0 {
		t.Fatalf("run(upload disable) = %d, want 0", code)
	}
	if code := run([]string{"sync"}); code != 0 {
		t.Fatalf("run(sync) = %d, want 0", code)
	}

	if got := requests.Load(); got != 0 {
		t.Fatalf("upload requests while disabled = %d, want 0", got)
	}
	if got := pendingEvents(t, home); got == 0 {
		t.Fatal("pending events = 0 while disabled, want the scan to have queued its events")
	}
}

// Re-enabling must send what accumulated while the switch was off — the
// queue is why disabling is not the same as losing data.
func TestSyncAfterEnableUploadsEventsQueuedWhileDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClaudeTranscript(t, home)
	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	var requests atomic.Int64
	countingUploadServer(t, &requests)

	if code := run([]string{"upload", "disable"}); code != 0 {
		t.Fatalf("run(upload disable) = %d, want 0", code)
	}
	if code := run([]string{"sync"}); code != 0 {
		t.Fatalf("run(sync) = %d, want 0", code)
	}
	queued := pendingEvents(t, home)
	if queued == 0 {
		t.Fatal("pending events = 0 after disabled sync, want queued events")
	}

	if code := run([]string{"upload", "enable"}); code != 0 {
		t.Fatalf("run(upload enable) = %d, want 0", code)
	}
	if code := run([]string{"sync"}); code != 0 {
		t.Fatalf("run(sync) = %d, want 0", code)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("upload requests after enable = 0, want the queued events to be sent")
	}
}

// The worker re-reads the switch every tick, so flipping it takes effect on a
// running service. A worker that cached the answer at start would keep
// uploading until someone restarted it.
func TestWorkerLoopHonoursSwitchWithoutRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := writeClaudeTranscript(t, home)
	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	var requests atomic.Int64
	countingUploadServer(t, &requests)

	if code := run([]string{"upload", "disable"}); code != 0 {
		t.Fatalf("run(upload disable) = %d, want 0", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flags := workerFlags{
		providerDirs:   map[agentlib.Provider][]string{agentlib.ProviderClaude: {claudeDir}},
		interval:       20 * time.Millisecond,
		uploadInterval: 20 * time.Millisecond,
	}
	workerDone := make(chan struct{})
	go func() {
		runWorkerLoop(ctx, flags)
		close(workerDone)
	}()

	// Long enough for many upload ticks: while disabled, none may reach the
	// server no matter how often the loop runs.
	time.Sleep(200 * time.Millisecond)
	if got := requests.Load(); got != 0 {
		t.Fatalf("upload requests while disabled = %d, want 0", got)
	}

	if code := run([]string{"upload", "enable"}); code != 0 {
		t.Fatalf("run(upload enable) = %d, want 0", code)
	}

	deadline := time.Now().Add(3 * time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("upload requests after enabling mid-run = 0, want the worker to pick the switch up without a restart")
	}

	cancel()
	<-workerDone
}
