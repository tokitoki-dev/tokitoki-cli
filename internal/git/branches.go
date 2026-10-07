package git

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Branches is which branch a checkout had checked out, over time: the branch
// it is on now and every switch its reflog recorded.
//
// It comes from disk, not from the tools that report the work: an agent
// records the branch of the folder it was started in, which is often not the
// checkout the work landed in, and most tools record none at all.
type Branches struct {
	current string
	// since is the oldest time the reflog speaks for. Zero when there is no
	// reflog: HEAD never moved, so current is all there ever was.
	since    time.Time
	switches []switched
}

// switched is HEAD leaving a branch.
type switched struct {
	at   time.Time
	from string
}

// Branches reads the checkout's HEAD and the reflog of it.
func (r Repo) Branches() Branches {
	if r.gitDir == "" {
		return Branches{}
	}
	branches := Branches{current: r.currentBranch()}

	data, err := os.ReadFile(filepath.Join(r.gitDir, "logs", "HEAD"))
	if errors.Is(err, fs.ErrNotExist) {
		return branches
	}
	if err != nil {
		// A reflog that cannot be read says nothing — not that HEAD never
		// moved, which would put every past event on today's branch.
		return Branches{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		at, message, ok := reflogEntry(line)
		if !ok {
			continue
		}
		if branches.since.IsZero() {
			branches.since = at
		}
		if from, ok := switchedFrom(message); ok {
			branches.switches = append(branches.switches, switched{at: at, from: from})
		}
	}
	return branches
}

// At reports the branch checked out at t: the current one with every switch
// made after t undone, newest first. Before the reflog's first entry nothing
// is known, and a detached HEAD is on no branch; both are "".
func (b Branches) At(t time.Time) string {
	if t.Before(b.since) {
		return ""
	}
	branch := b.current
	for i := len(b.switches) - 1; i >= 0 && b.switches[i].at.After(t); i-- {
		branch = b.switches[i].from
	}
	return branch
}

// currentBranch is the branch HEAD is on or, while a rebase has detached it,
// the branch being rebased: the one the work is going to.
func (r Repo) currentBranch() string {
	head, _ := firstLine(filepath.Join(r.gitDir, "HEAD"))
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		return branchRef(strings.TrimSpace(ref))
	}
	for _, rebase := range []string{"rebase-merge", "rebase-apply"} {
		if ref, ok := firstLine(filepath.Join(r.gitDir, rebase, "head-name")); ok {
			return branchRef(ref)
		}
	}
	return ""
}

// branchRef is the branch a ref names, or "" for any other ref and for
// refs/heads/.invalid, the placeholder a reftable repository keeps in HEAD.
func branchRef(ref string) string {
	name, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok || name == ".invalid" {
		return ""
	}
	return name
}

// reflogEntry splits a reflog line — "<old> <new> <name> <<email>> <unix
// seconds> <zone>\t<message>" — into its time and message. The message may be
// missing: git worktree add writes none.
func reflogEntry(line string) (time.Time, string, bool) {
	meta, message, _ := strings.Cut(line, "\t")
	fields := strings.Fields(meta)
	if len(fields) < 2 {
		return time.Time{}, "", false
	}
	seconds, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.Unix(seconds, 0), message, true
}

// switchedFrom reads the branch HEAD left from a reflog message. Checking out
// or switching writes "checkout: moving from <from> to <to>", <from> being a
// commit id when HEAD was detached. Renaming the branch checked out writes
// "Branch: renamed refs/heads/<from> to refs/heads/<to>"; renaming any other
// branch leaves HEAD's reflog alone. A rebase writes neither: the work done
// during one is on the branch being rebased.
func switchedFrom(message string) (string, bool) {
	if rest, ok := strings.CutPrefix(message, "checkout: moving from "); ok {
		from, _, ok := strings.Cut(rest, " to ")
		if isCommitID(from) {
			from = ""
		}
		return from, ok
	}
	if rest, ok := strings.CutPrefix(message, "Branch: renamed refs/heads/"); ok {
		from, _, ok := strings.Cut(rest, " to refs/heads/")
		return from, ok
	}
	return "", false
}

// isCommitID reports a full SHA-1 or SHA-256 object id.
func isCommitID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && strings.Trim(s, "0123456789abcdef") == ""
}
