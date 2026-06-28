package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/config"
)

const (
	UsageDBFile   = "usage.bolt"
	apiKeyFile    = "api_key"
	directoryMod  = 0o700
	apiKeyFileMod = 0o600
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

// LoadSettings reads the API key from the api_key file.
func (s *FileStore) LoadSettings() (agent.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(s.dir, apiKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return agent.Settings{}, nil
	}
	if err != nil {
		return agent.Settings{}, err
	}
	return agent.Settings{APIKey: strings.TrimSpace(string(data))}, nil
}

func (s *FileStore) SaveAPIKey(apiKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("API key cannot be empty")
	}
	path := filepath.Join(s.dir, apiKeyFile)
	if err := os.WriteFile(path, []byte(apiKey+"\n"), apiKeyFileMod); err != nil {
		return err
	}
	return os.Chmod(path, apiKeyFileMod)
}
