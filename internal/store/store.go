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
	configDirName = "config"
	dataDirName   = "data"
	stateDirName  = "state"

	UsageDBFile   = "tokitoki.db"
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
	if err := migrateOldDataStructure(dir); err != nil {
		return "", err
	}
	if err := ensureSubdirectoriesExist(dir); err != nil {
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

// UsageDBPath returns the path to the usage database file within the data directory.
func UsageDBPath(dataDir string) string {
	return filepath.Join(dataDir, dataDirName, UsageDBFile)
}

// LoadSettings reads the API key from the config/api_key file.
func (s *FileStore) LoadSettings() (agent.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(s.dir, configDirName, apiKeyFile))
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
	configDir := filepath.Join(s.dir, configDirName)
	if err := os.MkdirAll(configDir, directoryMod); err != nil {
		return err
	}
	path := filepath.Join(configDir, apiKeyFile)
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
	configDir := filepath.Join(s.dir, configDirName)
	if err := os.MkdirAll(configDir, directoryMod); err != nil {
		return err
	}
	return s.writeFileLocked(filepath.Join(configDir, apiKeyFile), apiKey)
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

func ensureSubdirectoriesExist(dir string) error {
	subdirs := []string{configDirName, dataDirName, stateDirName}
	for _, subdir := range subdirs {
		path := filepath.Join(dir, subdir)
		if err := os.MkdirAll(path, directoryMod); err != nil {
			return err
		}
	}
	return nil
}

// migrateOldDataStructure handles migration from flat structure to hierarchical.
// Old structure: ~/.tokitoki/{api_key, usage.db, *.lock}
// New structure: ~/.tokitoki/{config/api_key, data/tokitoki.db, state/*.lock}
func migrateOldDataStructure(dir string) error {
	// Migrate api_key
	oldAPIKeyPath := filepath.Join(dir, "api_key")
	newAPIKeyPath := filepath.Join(dir, configDirName, "api_key")
	if _, err := os.Stat(oldAPIKeyPath); err == nil && !fileExists(newAPIKeyPath) {
		if err := os.MkdirAll(filepath.Dir(newAPIKeyPath), directoryMod); err != nil {
			return err
		}
		if err := os.Rename(oldAPIKeyPath, newAPIKeyPath); err != nil {
			return err
		}
	}

	// Migrate usage.db
	oldDBPath := filepath.Join(dir, "usage.db")
	newDBPath := filepath.Join(dir, dataDirName, "tokitoki.db")
	if _, err := os.Stat(oldDBPath); err == nil && !fileExists(newDBPath) {
		if err := os.MkdirAll(filepath.Dir(newDBPath), directoryMod); err != nil {
			return err
		}
		if err := os.Rename(oldDBPath, newDBPath); err != nil {
			return err
		}
	}

	// Migrate lock files to state/
	lockFiles := []string{"tokitoki.lock", "upgrade.lock", "upload.lock"}
	for _, lockFile := range lockFiles {
		oldLockPath := filepath.Join(dir, lockFile)
		newLockPath := filepath.Join(dir, stateDirName, lockFile)
		if _, err := os.Stat(oldLockPath); err == nil && !fileExists(newLockPath) {
			if err := os.MkdirAll(filepath.Dir(newLockPath), directoryMod); err != nil {
				return err
			}
			if err := os.Rename(oldLockPath, newLockPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
