package statusbar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/statusbar/today" {
			t.Errorf("request = %s %s, want GET /api/statusbar/today", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tokitoki_test" {
			t.Errorf("Authorization = %q, want the API key as a bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"date":"2026-09-17","timezone":"Asia/Tokyo","scope":"team","team_name":"Acme","active_seconds":12204,"total_tokens":1830000,"text":"3h 23m"}`)
	}))
	defer server.Close()

	report, err := Fetch(context.Background(), server.URL, "tokitoki_test")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if report.Text != "3h 23m" || report.ActiveSeconds != 12204 || report.TeamName != "Acme" || report.Scope != "team" {
		t.Fatalf("Fetch() = %+v, want the server's report", report)
	}
	if report.Stale || report.FetchedAt.IsZero() {
		t.Fatalf("Fetch() = %+v, want a fresh report stamped with its fetch time", report)
	}
}

func TestFetchRejectsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.URL, "revoked")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Fetch() error = %v, want ErrUnauthorized", err)
	}
}

func TestFetchRejectsEmptyReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	if _, err := Fetch(context.Background(), server.URL, "k"); err == nil {
		t.Fatal("Fetch() error = nil, want failure on a report without a date")
	}
}

func TestSaveThenLoadIsStale(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Load(dir); ok {
		t.Fatal("Load() ok = true before anything was saved")
	}
	if err := Save(dir, Report{Date: "2026-09-17", Text: "1h 2m", ActiveSeconds: 3720}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	report, ok := Load(dir)
	if !ok {
		t.Fatal("Load() ok = false after Save()")
	}
	if !report.Stale || report.Text != "1h 2m" {
		t.Fatalf("Load() = %+v, want the saved report marked stale", report)
	}
}
