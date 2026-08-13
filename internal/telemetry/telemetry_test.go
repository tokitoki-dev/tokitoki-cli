package telemetry

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A fresh install pings immediately; a second run inside the interval is
// throttled by the stamp file; `set key` bypasses the throttle.
func TestPingThrottleAndPayload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv(OptOutEnv, "")

	var calls atomic.Int64
	var got payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/telemetry/ping" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("bad payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	MaybePing(testLogger(), server.URL)
	if calls.Load() != 1 {
		t.Fatalf("fresh install: want 1 ping, got %d", calls.Load())
	}
	if got.Platform == "" || got.Arch == "" || got.AppVersion == "" {
		t.Fatalf("payload missing fields: %+v", got)
	}
	if got.HasAPIKey {
		t.Fatalf("no key configured, has_api_key must be false")
	}

	MaybePing(testLogger(), server.URL)
	if calls.Load() != 1 {
		t.Fatalf("second run inside interval: want still 1 ping, got %d", calls.Load())
	}

	Ping(testLogger(), server.URL)
	if calls.Load() != 2 {
		t.Fatalf("unthrottled Ping: want 2 pings, got %d", calls.Load())
	}
}

func TestOptOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv(OptOutEnv, "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("opted out, but the server was called")
	}))
	defer server.Close()

	MaybePing(testLogger(), server.URL)
	Ping(testLogger(), server.URL)
}

// A configured install authenticates its ping with the same Bearer header the
// uploader sends, and reports has_api_key accordingly.
func TestPingAuthenticatesWhenKeyConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv(OptOutEnv, "")

	dir, err := store.InitializeDataDir()
	if err != nil {
		t.Fatal(err)
	}
	const key = "tokitoki_test_key"
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "api_key"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var auth string
	var got payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("bad payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	Ping(testLogger(), server.URL)
	if auth != "Bearer "+key {
		t.Fatalf("Authorization = %q, want %q", auth, "Bearer "+key)
	}
	if !got.HasAPIKey {
		t.Fatalf("key configured, has_api_key must be true")
	}
}
