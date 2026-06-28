package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/labx/tokitoki-agent/internal/config"
)

func TestDefaultDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, config.DataDirName)
	if dir != want {
		t.Fatalf("DefaultDataDir() = %q, want %q", dir, want)
	}
}

func TestLoadSettingsReadsAPIKeyFile(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	const key = "tokitoki_test_key"
	if err := os.WriteFile(filepath.Join(dir, apiKeyFile), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != key {
		t.Fatalf("LoadSettings().APIKey = %q, want %q", loaded.APIKey, key)
	}
}

func TestLoadSettingsEmptyWhenNoKeyFile(t *testing.T) {
	fileStore, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != "" {
		t.Fatalf("LoadSettings().APIKey = %q, want empty", loaded.APIKey)
	}
}

func TestSaveAPIKeyWritesKeyFile(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := fileStore.SaveAPIKey("  tokitoki_test_key  \n"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tokitoki_test_key\n" {
		t.Fatalf("api key file = %q, want trimmed key with newline", string(data))
	}
	info, err := os.Stat(filepath.Join(dir, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != apiKeyFileMod {
		t.Fatalf("api key file mode = %v, want %v", got, apiKeyFileMod)
	}
}

func TestSaveAPIKeyRejectsEmptyKey(t *testing.T) {
	fileStore, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := fileStore.SaveAPIKey(" \n\t "); err == nil {
		t.Fatal("SaveAPIKey() error = nil, want empty key error")
	}
}
