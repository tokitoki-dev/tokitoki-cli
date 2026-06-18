package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/config"
)

const (
	UsageDBFile  = "usage.bolt"
	apiKeyFile   = "api_key"
	configFile   = "config.json"
	fileMode     = 0o600
	directoryMod = 0o700
)

type FileStore struct {
	dir string
	mu  sync.Mutex
}

func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, config.DataDirName), nil
}

func InitializeDataDir() (string, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, directoryMod); err != nil {
		return "", err
	}
	return dir, nil
}

func Open(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, directoryMod); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) LoadSettings() (agent.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var settings agent.Settings
	data, err := os.ReadFile(filepath.Join(s.dir, configFile))
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err != nil {
		return settings, err
	} else if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}

	apiKey, err := s.apiKeyLocked()
	if err != nil {
		return settings, err
	}
	if apiKey != "" {
		settings.APIKey = apiKey
		return settings, nil
	}

	if settings.APIKey != "" {
		if err := s.saveAPIKeyLocked(settings.APIKey); err != nil {
			return settings, err
		}
		if err := s.saveConfigLocked(agent.Settings{
			ServerURL: settings.ServerURL,
		}); err != nil {
			return settings, err
		}
	}
	return settings, nil
}

func (s *FileStore) SaveSettings(settings agent.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.saveAPIKeyLocked(settings.APIKey); err != nil {
		return err
	}

	configSettings := settings
	configSettings.APIKey = ""
	return s.saveConfigLocked(configSettings)
}

func (s *FileStore) apiKeyLocked() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, apiKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *FileStore) saveAPIKeyLocked(apiKey string) error {
	path := filepath.Join(s.dir, apiKeyFile)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, []byte(apiKey+"\n"))
}

func (s *FileStore) saveConfigLocked(settings agent.Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return writeFileAtomic(filepath.Join(s.dir, configFile), append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, fileMode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
