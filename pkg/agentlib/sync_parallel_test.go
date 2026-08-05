package agentlib

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"compress/gzip"
	"encoding/json"
	"io"
)

// Uploading must begin while the scan is still running. A scan queues each
// file's events as it finishes that file, so waiting for the whole scan before
// sending anything leaves the network idle for the entire ingest.
func TestSyncUploadsWhileScanning(t *testing.T) {
	var firstUploadAt atomic.Int64
	var batches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batches.Add(1)
		firstUploadAt.CompareAndSwap(0, time.Now().UnixNano())
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			defer gz.Close()
			body = gz
		}
		var payload struct {
			Events []struct {
				ID string `json:"id"`
			}
		}
		json.NewDecoder(body).Decode(&payload)
		ids := make([]string, 0, len(payload.Events))
		for _, e := range payload.Events {
			ids = append(ids, e.ID)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "accepted": ids})
	}))
	defer srv.Close()
	t.Setenv("TOKITOKI_BASE_URL", srv.URL)

	dir := t.TempDir()
	// 很多文件, 让扫描明显耗时
	for i := 0; i < 400; i++ {
		p := filepath.Join(dir, "claude", "projects", fmt.Sprintf("-p-%03d", i))
		os.MkdirAll(p, 0o700)
		var buf []byte
		for j := 0; j < 300; j++ {
			buf = append(buf, []byte(fmt.Sprintf(`{"timestamp":"2026-06-04T01:02:03Z","cwd":"/p/%03d","requestId":"r%03d-%03d","message":{"id":"m%03d-%03d","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n", i, i, j, i, j))...)
		}
		os.WriteFile(filepath.Join(p, "s.jsonl"), buf, 0o600)
	}
	os.MkdirAll(filepath.Join(dir, "config"), 0o700)
	os.WriteFile(filepath.Join(dir, "config", "api_key"), []byte("k"), 0o600)

	client, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = client.Sync(context.Background(), SyncOptions{ProviderDirs: map[Provider][]string{
		ProviderClaude: {filepath.Join(dir, "claude")},
	}})
	total := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}

	if firstUploadAt.Load() == 0 {
		t.Fatal("从未上传")
	}
	firstAt := time.Unix(0, firstUploadAt.Load()).Sub(start)
	fmt.Printf("总耗时=%v  首次上传发生在=%v (%.0f%%处)  批次=%d\n",
		total.Round(time.Millisecond), firstAt.Round(time.Millisecond),
		float64(firstAt)/float64(total)*100, batches.Load())
}

// Everything the scan queued must be uploaded by the time Sync returns.
// Callers are one-shot processes that exit immediately afterwards, so an
// event still sitting in the queue is an event that waited for the next run.
func TestSyncUploadsEverythingBeforeReturning(t *testing.T) {
	var uploaded sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			defer gz.Close()
			body = gz
		}
		var payload struct {
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
		}
		json.NewDecoder(body).Decode(&payload)
		ids := make([]string, 0, len(payload.Events))
		for _, e := range payload.Events {
			uploaded.Store(e.ID, true)
			ids = append(ids, e.ID)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "accepted": ids})
	}))
	defer srv.Close()
	t.Setenv("TOKITOKI_BASE_URL", srv.URL)

	const files = 40
	dir := t.TempDir()
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, "claude", "projects", fmt.Sprintf("-p-%03d", i))
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"timestamp":"2026-06-04T01:02:03Z","cwd":"/p/%03d","requestId":"r%03d","message":{"id":"m%03d","model":"claude","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n", i, i, i)
		if err := os.WriteFile(filepath.Join(p, "s.jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "api_key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Sync(context.Background(), SyncOptions{ProviderDirs: map[Provider][]string{
		ProviderClaude: {filepath.Join(dir, "claude")},
	}}); err != nil {
		t.Fatal(err)
	}

	count := 0
	uploaded.Range(func(any, any) bool { count++; return true })
	if count != files {
		t.Fatalf("uploaded %d of %d events before Sync returned", count, files)
	}
}

// A scan that produces a steady trickle must not turn into a request per
// handful of events. The drain waits for a worthwhile batch and sends one
// batch per pass, so the queue refills between requests instead of being
// chased down to nothing.
func TestSyncBatchesTrickleIntoFewRequests(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			defer gz.Close()
			body = gz
		}
		var payload struct {
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
		}
		json.NewDecoder(body).Decode(&payload)
		ids := make([]string, 0, len(payload.Events))
		for _, e := range payload.Events {
			ids = append(ids, e.ID)
		}
		mu.Lock()
		sizes = append(sizes, len(payload.Events))
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "accepted": ids})
	}))
	defer srv.Close()
	t.Setenv("TOKITOKI_BASE_URL", srv.URL)

	// Many files holding one event each: a slow scan producing a trickle.
	const files = 3000
	dir := t.TempDir()
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, "claude", "projects", fmt.Sprintf("-p-%04d", i))
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"timestamp":"2026-06-04T01:02:03Z","cwd":"/p/%04d","requestId":"r%04d","message":{"id":"m%04d","model":"c","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n", i, i, i)
		if err := os.WriteFile(filepath.Join(p, "s.jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "api_key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Sync(context.Background(), SyncOptions{ProviderDirs: map[Provider][]string{
		ProviderClaude: {filepath.Join(dir, "claude")},
	}}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, s := range sizes {
		total += s
	}
	t.Logf("%d events in %d requests", total, len(sizes))
	if total != files {
		t.Fatalf("uploaded %d of %d events", total, files)
	}
	// Chasing the scan produced ~110 requests for this input; batching keeps
	// it near the number of full batches the queue can actually form.
	if len(sizes) > 10 {
		t.Errorf("%d requests for %d events: the drain is chasing the scan", len(sizes), total)
	}
}
