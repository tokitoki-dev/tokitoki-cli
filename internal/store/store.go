package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
)

const (
	UsageDBFile = "usage.db"
	apiKeyFile  = "api_key"
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
		if err := s.ensureAPIKeyFileLocked(); err != nil {
			return agent.Settings{}, err
		}
		return agent.Settings{}, nil
	}
	if err != nil {
		return agent.Settings{}, err
	}
	return agent.Settings{APIKey: strings.TrimSpace(string(data))}, nil
}

func (s *FileStore) EnsureAPIKeyFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.ensureAPIKeyFileLocked()
}

func (s *FileStore) ensureAPIKeyFileLocked() error {
	path := filepath.Join(s.dir, apiKeyFile)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, apiKeyFileMod)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, apiKeyFileMod)
}

func (s *FileStore) SaveAPIKey(apiKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return errors.New("API key cannot be empty")
	}
	return s.writeFileLocked(filepath.Join(s.dir, apiKeyFile), apiKey)
}

// writeFileLocked writes value+"\n" to path with owner-only permissions, via
// a temp file renamed into place so readers never see a torn write.
func (s *FileStore) writeFileLocked(path, value string) error {
	tmp, err := os.CreateTemp(s.dir, "."+filepath.Base(path)+".tmp.")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(value + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(apiKeyFileMod); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, apiKeyFileMod)
}
