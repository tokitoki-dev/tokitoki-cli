package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
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
	configDir := filepath.Join(dir, configDirName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, apiKeyFile), []byte(key+"\n"), 0o600); err != nil {
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
	dir := t.TempDir()
	fileStore, err := Open(dir)
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
	info, err := os.Stat(filepath.Join(dir, configDirName, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != apiKeyFileMod {
		t.Fatalf("api key file mode = %v, want %v", got, apiKeyFileMod)
	}
}

func TestEnsureAPIKeyFileCreatesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := fileStore.EnsureAPIKeyFile(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, configDirName, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "" {
		t.Fatalf("api key file = %q, want empty", string(data))
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

	data, err := os.ReadFile(filepath.Join(dir, configDirName, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tokitoki_test_key\n" {
		t.Fatalf("api key file = %q, want trimmed key with newline", string(data))
	}
	info, err := os.Stat(filepath.Join(dir, configDirName, apiKeyFile))
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

func TestLoadSettingsReadsHostnameFile(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, configDirName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, hostnameFile), []byte(" studio \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Hostname != "studio" {
		t.Fatalf("LoadSettings().Hostname = %q, want %q", loaded.Hostname, "studio")
	}
	// A missing key file must not hide the hostname: the two are independent.
	if loaded.APIKey != "" {
		t.Fatalf("LoadSettings().APIKey = %q, want empty", loaded.APIKey)
	}
}

func TestSaveHostnameWritesThenClears(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileStore.SaveHostname("  studio \n"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, configDirName, hostnameFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "studio\n" {
		t.Fatalf("hostname file = %q, want trimmed name", string(data))
	}

	// Empty is "back to the system name": the file goes away, and clearing
	// twice is not an error.
	for range 2 {
		if err := fileStore.SaveHostname(""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hostname file still present after clear: %v", err)
	}
	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Hostname != "" {
		t.Fatalf("LoadSettings().Hostname = %q after clear, want empty", loaded.Hostname)
	}
}

func TestSaveHostnameRejectsControlCharacters(t *testing.T) {
	fileStore, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := fileStore.SaveHostname("stu\ndio"); err == nil {
		t.Fatal("SaveHostname() error = nil, want control character error")
	}
}
