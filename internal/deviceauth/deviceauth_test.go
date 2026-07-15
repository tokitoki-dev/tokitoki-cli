package deviceauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/device-login" {
			t.Errorf("request = %s %s, want POST /api/auth/device-login", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tokitoki_test" {
			t.Errorf("Authorization = %q, want the API key as a bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"url":"https://tokitoki.example.com/api/auth/device-login?token=abc"}`)
	}))
	defer server.Close()

	url, err := DashboardURL(context.Background(), server.URL, "tokitoki_test")
	if err != nil {
		t.Fatalf("DashboardURL() error = %v", err)
	}
	if url != "https://tokitoki.example.com/api/auth/device-login?token=abc" {
		t.Fatalf("DashboardURL() = %q, want the server's login URL", url)
	}
}

func TestDashboardURLRejectsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, err := DashboardURL(context.Background(), server.URL, "revoked"); err == nil {
		t.Fatal("DashboardURL() error = nil, want failure on 401")
	}
}

func TestDashboardURLRejectsEmptyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"url":""}`)
	}))
	defer server.Close()

	if _, err := DashboardURL(context.Background(), server.URL, "tokitoki_test"); err == nil {
		t.Fatal("DashboardURL() error = nil, want failure on empty URL")
	}
}
