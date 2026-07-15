package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeBinary writes an executable that prints version when run with any
// arguments — a stand-in for a real tokitoki build.
func fakeBinary(t *testing.T, path, version string) []byte {
	t.Helper()
	content := []byte(fmt.Sprintf("#!/bin/sh\necho %s\n", version))
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return content
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testServer(t *testing.T, checkBody string, binary []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/updates/check", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("channel"); got != UpdateChannel {
			t.Errorf("check channel = %q, want %q", got, UpdateChannel)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, checkBody)
	})
	mux.HandleFunc("/api/updates/download/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binary)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestUpgradeReplacesExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}

	dir := t.TempDir()
	executable := filepath.Join(dir, "tokitoki")
	fakeBinary(t, executable, "1.0.0")
	newBinary := fakeBinary(t, filepath.Join(dir, "next"), "1.1.0")

	check := fmt.Sprintf(
		`{"available":true,"version":"1.1.0","url":"/api/updates/download/cli/1.1.0","size":%d}`,
		len(newBinary),
	)
	server := testServer(t, check, newBinary)

	result, err := upgradeExecutable(context.Background(), testLogger(), server.URL, "1.0.0", executable, dir)
	if err != nil {
		t.Fatalf("upgradeExecutable() error = %v", err)
	}
	if !result.Updated || result.Version != "1.1.0" {
		t.Fatalf("upgradeExecutable() = %+v, want updated to 1.1.0", result)
	}

	installed, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(newBinary) {
		t.Fatalf("executable was not replaced with the downloaded binary")
	}
}

func TestUpgradeNoUpdateAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}

	dir := t.TempDir()
	executable := filepath.Join(dir, "tokitoki")
	original := fakeBinary(t, executable, "1.0.0")
	server := testServer(t, `{"available":false}`, nil)

	result, err := upgradeExecutable(context.Background(), testLogger(), server.URL, "1.0.0", executable, dir)
	if err != nil {
		t.Fatalf("upgradeExecutable() error = %v", err)
	}
	if result.Updated || result.Version != "1.0.0" {
		t.Fatalf("upgradeExecutable() = %+v, want no update", result)
	}

	installed, _ := os.ReadFile(executable)
	if string(installed) != string(original) {
		t.Fatal("executable changed although no update was offered")
	}
}

func TestUpgradeRejectsVersionMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}

	dir := t.TempDir()
	executable := filepath.Join(dir, "tokitoki")
	original := fakeBinary(t, executable, "1.0.0")
	// The server claims 1.1.0 but serves a binary that still says 1.0.0 —
	// installing it would relaunch into an endless update loop.
	stale := fakeBinary(t, filepath.Join(dir, "stale"), "1.0.0")

	check := fmt.Sprintf(
		`{"available":true,"version":"1.1.0","url":"/api/updates/download/cli/1.1.0","size":%d}`,
		len(stale),
	)
	server := testServer(t, check, stale)

	if _, err := upgradeExecutable(context.Background(), testLogger(), server.URL, "1.0.0", executable, dir); err == nil {
		t.Fatal("upgradeExecutable() error = nil, want version-mismatch failure")
	}

	installed, _ := os.ReadFile(executable)
	if string(installed) != string(original) {
		t.Fatal("executable changed although verification failed")
	}
	entries, err := filepath.Glob(filepath.Join(dir, ".tokitoki-upgrade-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp download left behind: %v", entries)
	}
}

func TestUpgradeRefusesAppBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "TokiToki.app", "Contents", "Resources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "tokitoki")

	if _, err := upgradeExecutable(context.Background(), testLogger(), "http://unused.invalid", "1.0.0", executable, t.TempDir()); err == nil {
		t.Fatal("upgradeExecutable() error = nil, want refusal inside an app bundle")
	}
}

func TestUpgradeSkipsLocalBuild(t *testing.T) {
	result, err := Upgrade(context.Background(), testLogger(), "http://unused.invalid", "dev")
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if result.Updated {
		t.Fatal("Upgrade() updated a dev build")
	}
}
