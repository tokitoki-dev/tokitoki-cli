//go:build windows

package selfupdate

import "os"

// swap installs the new binary over the old one. Windows will not let a
// running executable be replaced in place, but it will let it be renamed —
// so the old binary steps aside and the new one takes its path. The .old
// leftover is deleted at the start of the next upgrade.
func swap(newBinary, executable string) error {
	old := executable + oldSuffix
	if err := os.Rename(executable, old); err != nil {
		return err
	}
	if err := os.Rename(newBinary, executable); err != nil {
		// Put the old binary back; a failed upgrade must not leave the
		// executable path empty.
		_ = os.Rename(old, executable)
		return err
	}
	return nil
}
