//go:build !windows

package selfupdate

import "os"

// swap installs the new binary over the old one. rename(2) is atomic within a
// filesystem, and a running process keeps its old inode — invocations in
// flight finish on the old code, the next invocation gets the new.
func swap(newBinary, executable string) error {
	return os.Rename(newBinary, executable)
}
