package agentlib

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Upload must be callable with nothing queued and no key configured: the
// upload loop runs on its own schedule and cannot assume a scan just ran.
func TestUploadWithoutScanOrKeyIsNotAnError(t *testing.T) {
	client, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Upload(context.Background()); err != nil {
		t.Fatalf("Upload on an empty queue = %v, want nil", err)
	}
}

// Scan must queue events without needing a key or an upload.
func TestScanQueuesWithoutUploading(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "claude", "projects", "-tmp-p")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-06-04T01:02:03Z","cwd":"/tmp/p","requestId":"r1","message":{"id":"m1","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Scan(SyncOptions{ProviderDirs: map[Provider][]string{
		ProviderClaude: {filepath.Join(dir, "claude")},
	}})
	if err != nil {
		t.Fatalf("Scan = %v, want nil", err)
	}
	if err := client.Scan(SyncOptions{}); err != ErrNoScanDirectories {
		t.Fatalf("Scan with no dirs = %v, want ErrNoScanDirectories", err)
	}
}
