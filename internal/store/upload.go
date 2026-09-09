package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DisabledFile is the sentinel whose existence suspends uploads. The file's
// existence is the whole record: there is nothing inside it to parse, so
// there is no such thing as a corrupt or half-written switch, and a user can
// flip it with touch and rm when no CLI is handy.
const DisabledFile = "upload-disabled"

// UploadDisabled reports whether uploads are suspended.
//
// A stat error other than "does not exist" counts as disabled: a state
// directory we cannot read is not one to upload from, and refusing to send is
// always recoverable while sending is not.
func UploadDisabled(dataDir string) bool {
	_, err := os.Stat(StatePath(dataDir, DisabledFile))
	return !errors.Is(err, os.ErrNotExist)
}

// DisableUpload creates the sentinel. Calling it on an already-disabled
// install succeeds and changes nothing.
func DisableUpload(dataDir string) error {
	path := StatePath(dataDir, DisabledFile)
	if err := os.MkdirAll(filepath.Dir(path), directoryMod); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, apiKeyFileMod)
	if err != nil {
		return fmt.Errorf("disable upload: %w", err)
	}
	return file.Close()
}

// EnableUpload removes the sentinel. A missing file is success: the caller
// asked for uploads to be on, and they are.
func EnableUpload(dataDir string) error {
	err := os.Remove(StatePath(dataDir, DisabledFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("enable upload: %w", err)
	}
	return nil
}
