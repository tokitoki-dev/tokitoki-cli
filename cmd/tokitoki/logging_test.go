package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
	"github.com/tokitoki-dev/tokitoki-cli/pkg/agentlib"
)

// Error is reserved for an install that is broken and that only this machine
// can know about. Everything a retry or the user's next step fixes is a
// warning — including a server error, which the server has its own record of.
func TestFailureLevelKeepsErrorForLocalFaults(t *testing.T) {
	transport := &url.Error{Op: "Post", URL: "https://tokitoki.dev", Err: errors.New("no route to host")}
	status := &usageupload.StatusError{Code: 401, Status: "401 Unauthorized"}

	warnings := map[string]error{
		"no api key":           agentlib.ErrMissingAPIKey,
		"network down":         transport,
		"server said 401":      status,
		"server said 500":      &usageupload.StatusError{Code: 500, Status: "500 Internal Server Error"},
		"run out of time":      context.DeadlineExceeded,
		"cancelled":            context.Canceled,
		"wrapped network":      fmt.Errorf("sync: %w", transport),
		"joined with a status": errors.Join(nil, status),
	}
	for name, err := range warnings {
		if got := failureLevel(err); got != slog.LevelWarn {
			t.Errorf("%s: level %v, want WARN", name, got)
		}
	}

	faults := map[string]error{
		"queue will not open":  errors.New("open usage db: unable to open database file"),
		"data lock never free": fmt.Errorf("lock data dir: %w", store.ErrLockBusy),
		"state not writable":   &os.PathError{Op: "open", Path: "/x", Err: os.ErrPermission},
	}
	for name, err := range faults {
		if got := failureLevel(err); got != slog.LevelError {
			t.Errorf("%s: level %v, want ERROR", name, got)
		}
	}
}

func TestRunLeavesARecordThatNeverHoldsTheKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TOKITOKI_NO_TELEMETRY", "1")

	const key = "tokitoki_secret_key_value"
	if code := run([]string{"set", "key", key}); code != 0 {
		t.Fatalf("set key exited %d", code)
	}
	if code := run([]string{"get", "hostname", "extra"}); code != 2 {
		t.Fatalf("usage error exited %d, want 2", code)
	}

	raw, err := os.ReadFile(store.LogPath(home+"/"+config.DataDirName, store.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if strings.Contains(log, key) {
		t.Fatalf("the API key was written to the log:\n%s", log)
	}
	for _, want := range []string{
		`"msg":"run started"`, `"cmd":"set"`,
		`"msg":"run finished"`, `"exit_code":0`,
		`"cmd":"get"`, `"exit_code":2`, `"duration_ms":`, `"pid":`, `"version":`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s:\n%s", want, log)
		}
	}
}

// help and version do no work and must not create a data directory just to
// say so.
func TestHelpAndVersionLeaveNoTrace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	run([]string{"version"})
	run([]string{"help"})
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("help/version created %v under home", entries)
	}
}
