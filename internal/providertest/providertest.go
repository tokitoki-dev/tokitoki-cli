// Package providertest holds the fixtures and assertions provider packages
// share in their tests. It lives apart from usageprovider so no production
// package ever links in the testing package.
package providertest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// WantEntry describes the single entry a provider must produce from a minimal
// fixture. Every provider asserts the same shape, so the check lives here
// rather than once per package.
type WantEntry struct {
	Provider  usage.Provider
	Model     string
	SessionID string
	Project   string
	Tokens    usage.TokenUsage
}

// AssertSingleEntry checks that a provider loaded exactly one entry matching
// want. It is the shared body of every provider's smoke test.
func AssertSingleEntry(t *testing.T, entries []usage.Entry, err error, want WantEntry) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1: %#v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Provider != want.Provider {
		t.Fatalf("provider = %q, want %q", entry.Provider, want.Provider)
	}
	if entry.Model != want.Model {
		t.Fatalf("model = %q, want %q", entry.Model, want.Model)
	}
	if entry.SessionID != want.SessionID {
		t.Fatalf("session id = %q, want %q", entry.SessionID, want.SessionID)
	}
	if entry.Project != want.Project {
		t.Fatalf("project = %q, want %q", entry.Project, want.Project)
	}
	if entry.Usage != want.Tokens {
		t.Fatalf("usage = %#v, want %#v", entry.Usage, want.Tokens)
	}
	if entry.ID == "" {
		t.Fatal("ID is empty")
	}
}

// WriteFile writes a fixture file, creating its directory.
func WriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// OpenTestSQLite opens a fixture database, creating its directory.
func OpenTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := agentdb.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// ExecSQL runs a fixture statement and fails the test if it errors.
func ExecSQL(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}
