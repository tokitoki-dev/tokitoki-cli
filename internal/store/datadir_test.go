package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
)

// A stamp must move every piece of state, not just the top directory: a build
// that shared the database or the lock with another would defeat the point of
// a separate data dir. Arbitrary names are used here because the value is a
// build parameter — the code must treat every well-formed name alike.
func TestStampMovesAllState(t *testing.T) {
	original := config.DataDirName
	t.Cleanup(func() { config.DataDirName = original })

	for _, name := range []string{".tokitoki", ".tokitoki-dev", ".tokitoki-staging"} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		config.DataDirName = name

		dir, err := DefaultDataDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, name); dir != want {
			t.Fatalf("DefaultDataDir() = %q, want %q", dir, want)
		}

		for label, path := range map[string]string{
			"usage database": UsageDBPath(dir),
			"upload switch":  StatePath(dir, DisabledFile),
			"data lock":      StatePath(dir, LockFile),
		} {
			if !underDir(path, dir) {
				t.Errorf("%s at %q, want it under %q", label, path, dir)
			}
		}

		// The API key too — the one that decides whose account gets the data.
		fileStore, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := fileStore.SaveAPIKey("key_for_" + name); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, configDirName, apiKeyFile)); err != nil {
			t.Fatalf("api key not in the stamped dir %q: %v", name, err)
		}

		// Nothing may appear outside the stamped directory.
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() != name {
				t.Errorf("build stamped %q also created %q in home", name, entry.Name())
			}
		}
	}
}

// Two differently stamped builds must not see each other's state — the whole
// reason the directory is a build parameter.
func TestDifferentStampsShareNothing(t *testing.T) {
	original := config.DataDirName
	t.Cleanup(func() { config.DataDirName = original })

	home := t.TempDir()
	t.Setenv("HOME", home)

	saveKey := func(stamp, key string) string {
		config.DataDirName = stamp
		dir, err := DefaultDataDir()
		if err != nil {
			t.Fatal(err)
		}
		fileStore, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := fileStore.SaveAPIKey(key); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	readKey := func(stamp string) string {
		config.DataDirName = stamp
		dir, err := DefaultDataDir()
		if err != nil {
			t.Fatal(err)
		}
		fileStore, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		settings, err := fileStore.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		return settings.APIKey
	}

	saveKey(".tokitoki", "installed_key")
	saveKey(".tokitoki-dev", "development_key")

	if got := readKey(".tokitoki"); got != "installed_key" {
		t.Fatalf("installed build reads key %q, want its own", got)
	}
	if got := readKey(".tokitoki-dev"); got != "development_key" {
		t.Fatalf("development build reads key %q, want its own", got)
	}
}

// A mis-stamped binary must fail where it resolves its directory, before it
// writes anything anywhere.
func TestDefaultDataDirRejectsBadStamp(t *testing.T) {
	original := config.DataDirName
	t.Cleanup(func() { config.DataDirName = original })

	config.DataDirName = "/etc"
	if _, err := DefaultDataDir(); err == nil {
		t.Fatal("DefaultDataDir() error = nil for an absolute stamp, want a rejection")
	}
}

func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
