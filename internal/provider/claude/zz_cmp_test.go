package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompareWithCcusage(t *testing.T) {
	day := os.Getenv("DAY")
	if day == "" { t.Skip() }
	root := os.Getenv("SNAP2")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude")
	}
	files := UsageFiles([]string{root}, "")

	seen := map[string]bool{}
	var in, out, cc, cr uint64
	n := 0
	for _, f := range files {
		loaded, _, err := ReadUsageFileFrom(f, 0)
		if err != nil { t.Fatal(err) }
		for _, e := range ConvertEntries(loaded) {
			if e.Timestamp.In(time.Local).Format("2006-01-02") != day { continue }
			if seen[e.ID] { continue }   // 跨文件去重, 同 ccusage
			seen[e.ID] = true
			n++
			in += e.Usage.InputTokens
			out += e.Usage.OutputTokens
			cc += e.Usage.CacheCreationInputTokens
			cr += e.Usage.CacheReadInputTokens
		}
	}
	fmt.Printf("TOKITOKI %s: events=%d in=%d out=%d cc=%d cr=%d total=%d\n",
		day, n, in, out, cc, cr, in+out+cc+cr)
}
