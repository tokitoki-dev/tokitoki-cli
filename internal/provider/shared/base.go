package shared

import (
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Base carries the scan configuration every provider needs: where to look and
// which files are already ingested. Providers embed it so the accessors and
// the WithPaths/WithFileFilter plumbing exist in one place instead of once per
// provider.
type Base struct {
	paths  []string
	filter usage.FileFilter
}

// NewBase returns a Base scanning the given data roots.
func NewBase(paths []string) Base {
	return Base{paths: append([]string{}, paths...)}
}

// Paths returns the data roots to scan.
func (b Base) Paths() []string { return b.paths }

// Filter returns the file filter, or nil when every file must be parsed.
func (b Base) Filter() usage.FileFilter { return b.filter }

// WithPathsSet returns a copy scanning the given data roots.
func (b Base) WithPathsSet(paths []string) Base {
	b.paths = append([]string{}, paths...)
	return b
}

// WithFilterSet returns a copy that skips the source files filter rejects.
func (b Base) WithFilterSet(filter usage.FileFilter) Base {
	b.filter = filter
	return b
}
