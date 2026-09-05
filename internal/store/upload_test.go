package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadEnabledByDefault(t *testing.T) {
	if UploadDisabled(t.TempDir()) {
		t.Fatal("UploadDisabled() = true on a fresh data dir, want false")
	}
}

func TestDisableUploadCreatesSentinel(t *testing.T) {
	dir := t.TempDir()
	if err := DisableUpload(dir); err != nil {
		t.Fatal(err)
	}
	if !UploadDisabled(dir) {
		t.Fatal("UploadDisabled() = false after DisableUpload(), want true")
	}
	if _, err := os.Stat(filepath.Join(dir, stateDirName, DisabledFile)); err != nil {
		t.Fatalf("stat sentinel: %v", err)
	}
}

// Both switches are idempotent: front-ends and scripts flip them without
// first asking what the current state is.
func TestUploadSwitchIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for range 2 {
		if err := DisableUpload(dir); err != nil {
			t.Fatalf("DisableUpload() error = %v, want nil", err)
		}
	}
	if !UploadDisabled(dir) {
		t.Fatal("UploadDisabled() = false after two disables, want true")
	}
	for range 2 {
		if err := EnableUpload(dir); err != nil {
			t.Fatalf("EnableUpload() error = %v, want nil", err)
		}
	}
	if UploadDisabled(dir) {
		t.Fatal("UploadDisabled() = true after two enables, want false")
	}
}

// The sentinel is a file the user may also create by hand, so whatever it
// contains must not change the answer.
func TestUploadDisabledIgnoresSentinelContents(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, stateDirName)
	if err := os.MkdirAll(stateDir, directoryMod); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, DisabledFile), []byte("off until monday"), apiKeyFileMod); err != nil {
		t.Fatal(err)
	}
	if !UploadDisabled(dir) {
		t.Fatal("UploadDisabled() = false for a non-empty sentinel, want true")
	}
}
