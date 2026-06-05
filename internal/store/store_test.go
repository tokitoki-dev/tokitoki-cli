package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/config"
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

func TestSaveSettingsStoresAPIKeySeparately(t *testing.T) {
	fileStore, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	settings := agent.Settings{
		APIKey:    "tokitoki_test_key",
		ServerURL: "http://127.0.0.1:9093",
	}
	if err := fileStore.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	apiKey, err := os.ReadFile(filepath.Join(fileStore.dir, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(apiKey)) != settings.APIKey {
		t.Fatalf("api key file = %q, want %q", string(apiKey), settings.APIKey)
	}

	config, err := os.ReadFile(filepath.Join(fileStore.dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), settings.APIKey) {
		t.Fatalf("config should not contain api key: %s", string(config))
	}

	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != settings {
		t.Fatalf("LoadSettings() = %#v, want %#v", loaded, settings)
	}
}

func TestLoadSettingsMigratesAPIKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	settings := agent.Settings{
		APIKey:    "tokitoki_legacy_key",
		ServerURL: "http://127.0.0.1:9093",
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), data, fileMode); err != nil {
		t.Fatal(err)
	}

	loaded, err := fileStore.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != settings {
		t.Fatalf("LoadSettings() = %#v, want %#v", loaded, settings)
	}

	apiKey, err := os.ReadFile(filepath.Join(dir, apiKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(apiKey)) != settings.APIKey {
		t.Fatalf("api key file = %q, want %q", string(apiKey), settings.APIKey)
	}

	config, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), settings.APIKey) {
		t.Fatalf("config should not contain migrated api key: %s", string(config))
	}
}
