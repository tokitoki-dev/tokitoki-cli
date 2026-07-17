package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
)

const (
	UsageDBFile   = "usage.db"
	apiKeyFile    = "api_key"
	installIDFile = "installation_id"
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

// LoadSettings reads the API key from the api_key file and this install's
// stable identity, generating the latter on first use.
func (s *FileStore) LoadSettings() (agent.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	installID, err := s.installationIDLocked()
	if err != nil {
		return agent.Settings{}, err
	}

	data, err := os.ReadFile(filepath.Join(s.dir, apiKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		if err := s.ensureAPIKeyFileLocked(); err != nil {
			return agent.Settings{}, err
		}
		return agent.Settings{InstallationID: installID}, nil
	}
	if err != nil {
		return agent.Settings{}, err
	}
	return agent.Settings{
		APIKey:         strings.TrimSpace(string(data)),
		InstallationID: installID,
	}, nil
}

// installationIDLocked returns the install's stable random identity, minting
// and persisting one the first time it is asked for. The server keys device
// rows on this value, so it must never change once written.
func (s *FileStore) installationIDLocked() (string, error) {
	path := filepath.Join(s.dir, installIDFile)
	data, err := os.ReadFile(path)
	if err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate installation id: %w", err)
	}
	id := hex.EncodeToString(raw)
	if err := s.writeFileLocked(path, id); err != nil {
		return "", fmt.Errorf("persist installation id: %w", err)
	}
	return id, nil
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
