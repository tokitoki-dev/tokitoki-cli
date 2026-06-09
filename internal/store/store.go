package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/config"
)

const (
	UsageDBFile  = "usage.bolt"
	apiKeyFile   = "api_key"
	configFile   = "config.json"
	queueFile    = "queue.jsonl"
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
	if err := migrateLegacyDataDir(dir); err != nil {
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

func (s *FileStore) AppendHeartbeat(heartbeat agent.Heartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(heartbeat)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(filepath.Join(s.dir, queueFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}

	return file.Sync()
}

func (s *FileStore) Heartbeats() ([]agent.Heartbeat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readHeartbeatsLocked()
}

func (s *FileStore) ReplaceHeartbeats(heartbeats []agent.Heartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, queueFile)
	if len(heartbeats) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	var data []byte
	for _, heartbeat := range heartbeats {
		line, err := json.Marshal(heartbeat)
		if err != nil {
			return err
		}
		data = append(data, line...)
		data = append(data, '\n')
	}

	return writeFileAtomic(path, data)
}

func (s *FileStore) QueueSize() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	heartbeats, err := s.readHeartbeatsLocked()
	if err != nil {
		return 0, err
	}

	return len(heartbeats), nil
}

func (s *FileStore) readHeartbeatsLocked() ([]agent.Heartbeat, error) {
	file, err := os.Open(filepath.Join(s.dir, queueFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var heartbeats []agent.Heartbeat
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var heartbeat agent.Heartbeat
		if err := json.Unmarshal(scanner.Bytes(), &heartbeat); err != nil {
			return nil, err
		}
		heartbeats = append(heartbeats, heartbeat)
	}

	return heartbeats, scanner.Err()
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

func migrateLegacyDataDir(newDir string) error {
	legacyDir, err := legacyDataDir()
	if err != nil {
		return err
	}

	same, err := samePath(newDir, legacyDir)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	if _, err := os.Stat(legacyDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	for _, name := range []string{configFile, queueFile, apiKeyFile, UsageDBFile} {
		if err := copyIfMissing(filepath.Join(legacyDir, name), filepath.Join(newDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func legacyDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TrackLM"), nil
}

func samePath(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	return absA == absB, nil
}

func copyIfMissing(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	srcFile, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return dstFile.Sync()
}
