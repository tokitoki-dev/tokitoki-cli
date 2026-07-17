package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const LockFile = "tokitoki.lock"

// UploadLockFile serializes queue drains across processes, separately from
// the data lock: draining talks to the network for up to the whole upload
// timeout, and ingestion must never wait behind that.
const UploadLockFile = "upload.lock"

// ErrLockBusy reports that another process held the lock for the whole
// timeout. Callers that treat "someone else is already doing this work" as
// success test for it with errors.Is.
var ErrLockBusy = errors.New("another TokiToki command is still running")

type DataLock struct {
	file *os.File
}

func AcquireDataLock(dir string, timeout time.Duration) (*DataLock, error) {
	lock, err := AcquireLock(dir, LockFile, timeout)
	if err != nil {
		return nil, fmt.Errorf("lock data dir: %w", err)
	}
	return lock, nil
}

// AcquireLock takes an exclusive advisory lock on dir/name, waiting up to
// timeout before giving up with ErrLockBusy.
func AcquireLock(dir, name string, timeout time.Duration) (*DataLock, error) {
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err = lockFile(file)
		if err == nil {
			return &DataLock{file: file}, nil
		}
		if !isLockBusy(err) {
			_ = file.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, ErrLockBusy
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
