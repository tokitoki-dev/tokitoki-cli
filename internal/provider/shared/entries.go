package shared

import (
	"sort"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// SortEntriesByTimestampDesc orders entries newest first, the order every
// provider returns them in.
func SortEntriesByTimestampDesc(entries []usage.Entry) []usage.Entry {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	return entries
}
