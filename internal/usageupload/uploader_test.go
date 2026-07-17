package usageupload

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

func TestDefaultServerURLIsLocalhost(t *testing.T) {
	if DefaultServerURL != "http://localhost:9093" {
		t.Fatalf("DefaultServerURL = %q, want http://localhost:9093", DefaultServerURL)
	}
}

func TestBaseURLDefaultsToLocalhost(t *testing.T) {
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
