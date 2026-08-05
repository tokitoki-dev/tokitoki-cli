package usageupload

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
)

func TestDefaultServerURLIsProduction(t *testing.T) {
	if DefaultServerURL != "https://tokitoki.dev" {
		t.Fatalf("DefaultServerURL = %q, want https://tokitoki.dev", DefaultServerURL)
	}
}

func TestBaseURLDefaultsToProduction(t *testing.T) {
	t.Setenv(BaseURLEnv, "")

	if got := BaseURL(); got != DefaultServerURL {
		t.Fatalf("BaseURL() = %q, want %q", got, DefaultServerURL)
	}
}

func TestBaseURLUsesEnvironment(t *testing.T) {
	t.Setenv(BaseURLEnv, " https://tokitoki.example.com/ ")

	if got := BaseURL(); got != "https://tokitoki.example.com" {
		t.Fatalf("BaseURL() = %q, want environment URL without trailing slash", got)
	}
}

func TestUploadUsesBaseURLEnvironment(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/usage-events/batch" {
			t.Fatalf("request path = %q, want /api/usage-events/batch", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(Response{
			OK:       true,
			BatchID:  "batch-1",
			Accepted: []string{"event-1"},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	t.Setenv(BaseURLEnv, server.URL+"/")

	resp, err := Upload(context.Background(), agent.Settings{APIKey: "test-key"}, []usage.Entry{{
		ID:        "event-1",
		Provider:  usage.ProviderCodex,
		Timestamp: time.Date(2026, 7, 1, 1, 2, 3, 0, time.UTC),
		Project:   "tracklm",
		Language:  "Go",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("test server was not called")
	}
	if resp.BatchID != "batch-1" || len(resp.Accepted) != 1 || resp.Accepted[0] != "event-1" {
		t.Fatalf("response = %+v, want accepted event", resp)
	}
}

func TestRelativeEntityHidesMachineLayout(t *testing.T) {
	tests := []struct {
		projectPath string
		entity      string
		want        string
	}{
		{"/Users/me/repo", "/Users/me/repo/pkg/a.go", "pkg/a.go"},
		{"/Users/me/repo", "/Users/me/repo/a.go", "a.go"},
		{"/Users/me/repo", "/Users/me/elsewhere/b.go", "b.go"},
		{"", "/Users/me/repo/c.go", "c.go"},
		{"/Users/me/repo", "", ""},
	}
	for _, tt := range tests {
		if got := relativeEntity(tt.projectPath, tt.entity); got != tt.want {
			t.Fatalf("relativeEntity(%q, %q) = %q, want %q", tt.projectPath, tt.entity, got, tt.want)
		}
	}
}

func TestRelativeFilesStripsMachineLayout(t *testing.T) {
	files := relativeFiles("/Users/me/repo", []usage.FileChange{
		{Path: "/Users/me/repo/pkg/a.go", LinesAdded: 2},
		{Path: "/Users/me/other/b.go", LinesRemoved: 1},
	})
	if files[0].Path != "pkg/a.go" || files[0].LinesAdded != 2 {
		t.Fatalf("files[0] = %+v, want pkg/a.go +2", files[0])
	}
	if files[1].Path != "b.go" || files[1].LinesRemoved != 1 {
		t.Fatalf("files[1] = %+v, want b.go -1", files[1])
	}
	if relativeFiles("/p", nil) != nil {
		t.Fatal("relativeFiles(nil) should be nil")
	}
}

// TestSyncPendingKeepsRejectionsVisible pins the queue's memory of what the
// server threw away. Recording rejections as uploaded once hid a server that
// refused every event from a whole provider: the sync looked clean and the
// data simply never appeared.
func TestSyncPendingKeepsRejectionsVisible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Response{
			OK:       true,
			Accepted: []string{"keep-me"},
			Rejected: []Reject{{ID: "drop-me", Reason: "AI provider must be claude or codex"}},
		})
	}))
	defer server.Close()
	t.Setenv(BaseURLEnv, server.URL)

	db, err := usagedb.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if _, err := db.InsertEvents([]usage.Entry{
		{ID: "keep-me", Provider: usage.ProviderClaude, Timestamp: now, Project: "demo"},
		{ID: "drop-me", Provider: usage.ProviderOpenCode, Timestamp: now, Project: "demo"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := SyncPending(context.Background(), agent.Settings{APIKey: "test-key"}, db); err != nil {
		t.Fatal(err)
	}

	// Neither event may be retried: one landed, the other was refused for good.
	pending, err := db.PendingEvents(now.Add(24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}

	// Pruning removes uploaded events and leaves rejected ones behind, so what
	// survives says which status each event carries.
	pruned, err := db.PruneUploaded(now.Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (only the accepted event is uploaded)", pruned)
	}
	// The rejected event is still queued as rejected rather than gone or retried.
	if _, err := db.InsertEvents([]usage.Entry{
		{ID: "drop-me", Provider: usage.ProviderOpenCode, Timestamp: now, Project: "demo"},
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingEvents(now.Add(48*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want none — a rejected event must not come back", pending)
	}
}
