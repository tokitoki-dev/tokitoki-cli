package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const LockFile = "tokitoki.lock"

type DataLock struct {
	file *os.File
}

func AcquireDataLock(dir string, timeout time.Duration) (*DataLock, error) {
	path := filepath.Join(dir, LockFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open data lock: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err = lockFile(file)
		if err == nil {
			return &DataLock{file: file}, nil
		}
		if !isLockBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("lock data dir: %w", err)
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock data dir: another TokiToki command is still running")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (l *DataLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
