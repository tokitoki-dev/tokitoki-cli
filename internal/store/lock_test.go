package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireDataLockExcludesOtherProcesses(t *testing.T) {
	if os.Getenv("TOKITOKI_STORE_LOCK_HELPER") == "1" {
		holdDataLockForTest(t)
		return
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "locked")
	cmd := exec.Command(os.Args[0], "-test.run=TestAcquireDataLockExcludesOtherProcesses")
	cmd.Env = append(os.Environ(),
		"TOKITOKI_STORE_LOCK_HELPER=1",
		"TOKITOKI_STORE_LOCK_DIR="+dir,
		"TOKITOKI_STORE_LOCK_MARKER="+marker,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	waitForFile(t, marker)
	lock, err := AcquireDataLock(dir, 50*time.Millisecond)
	if err == nil {
		_ = lock.Close()
		t.Fatal("AcquireDataLock() error = nil, want timeout while another process holds lock")
	}
}

func holdDataLockForTest(t *testing.T) {
	t.Helper()
	lock, err := AcquireDataLock(os.Getenv("TOKITOKI_STORE_LOCK_DIR"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	if err := os.WriteFile(os.Getenv("TOKITOKI_STORE_LOCK_MARKER"), []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
