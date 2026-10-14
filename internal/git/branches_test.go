package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	commitA = "1111111111111111111111111111111111111111"
	commitB = "2222222222222222222222222222222222222222"
)

// The reflog is the checkout's own diary of HEAD; reading it backwards from
// today's branch tells which branch any moment of work was on.
func TestBranchesAtUndoesTheSwitchesAfterIt(t *testing.T) {
	repo := checkoutWithHead(t, "ref: refs/heads/fix/renamed")
	writeReflog(t, repo,
		reflogLine(1000, "commit (initial): first"),
		reflogLine(2000, "checkout: moving from main to feature/login"),
		reflogLine(2500, "commit: login"),
		reflogLine(3000, "checkout: moving from feature/login to main"),
		reflogLine(4000, "checkout: moving from main to "+commitB),
		reflogLine(5000, "checkout: moving from "+commitB+" to fix/y"),
		reflogLine(6000, "Branch: renamed refs/heads/fix/y to refs/heads/fix/renamed"),
		reflogLine(6000, "Branch: renamed refs/heads/fix/y to refs/heads/fix/renamed"),
	)
	branches := mustFind(t, repo).Branches()

	for _, c := range []struct {
		at   int64
		want string
	}{
		{999, ""}, // before the reflog begins nothing is known
		{1000, "main"},
		{2000, "feature/login"},
		{2999, "feature/login"},
		{3500, "main"},
		{4500, ""}, // detached
		{5500, "fix/y"},
		{6000, "fix/renamed"},
		{time.Now().Unix(), "fix/renamed"},
	} {
		if got := branches.At(time.Unix(c.at, 0)); got != c.want {
			t.Errorf("At(%d) = %q, want %q", c.at, got, c.want)
		}
	}
}

// A repository with nothing committed yet has no reflog: HEAD has never
// moved, so the branch it names is the only one there has been.
func TestBranchesWithoutReflogIsTheCurrentBranch(t *testing.T) {
	repo := checkoutWithHead(t, "ref: refs/heads/trunk")

	if got := mustFind(t, repo).Branches().At(time.Unix(1, 0)); got != "trunk" {
		t.Fatalf("At() = %q, want trunk", got)
	}
}

func TestBranchesHeadsThatAreNoBranch(t *testing.T) {
	for name, head := range map[string]string{
		"detached":         commitA,
		"reftable":         "ref: refs/heads/.invalid",
		"not a branch ref": "ref: refs/remotes/origin/main",
	} {
		t.Run(name, func(t *testing.T) {
			repo := checkoutWithHead(t, head)
			if got := mustFind(t, repo).Branches().At(time.Now()); got != "" {
				t.Fatalf("At() = %q, want no branch", got)
			}
		})
	}
}

// A rebase detaches HEAD until it finishes; the work done meanwhile, conflict
// resolution above all, is on the branch being rebased.
func TestBranchesRebaseInProgressIsTheBranchBeingRebased(t *testing.T) {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		t.Run(dir, func(t *testing.T) {
			repo := checkoutWithHead(t, commitA)
			mustMkdirAll(t, filepath.Join(repo, ".git", dir))
			mustWriteFile(t, filepath.Join(repo, ".git", dir, "head-name"), "refs/heads/feature/login\n")
			writeReflog(t, repo,
				reflogLine(1000, "checkout: moving from main to feature/login"),
				reflogLine(2000, "rebase (start): checkout main"),
				reflogLine(2001, "rebase (pick): login"),
			)

			if got := mustFind(t, repo).Branches().At(time.Unix(2500, 0)); got != "feature/login" {
				t.Fatalf("At() = %q, want feature/login", got)
			}
		})
	}
}

// Every worktree has a HEAD of its own; the main checkout's says nothing
// about the work done in another.
func TestBranchesWorktreeReadsItsOwnHead(t *testing.T) {
	repo := checkoutWithHead(t, "ref: refs/heads/main")
	worktree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	linkWorktree(t, filepath.Join(repo, ".git"), worktree, "fix-login", true)

	if got := mustFind(t, filepath.Join(worktree, "src")).Branches().At(time.Now()); got != "fix-login" {
		t.Fatalf("At() = %q, want fix-login", got)
	}
}

func TestBranchesSubmoduleReadsItsOwnHead(t *testing.T) {
	super := checkoutWithHead(t, "ref: refs/heads/main")
	modules := filepath.Join(super, ".git", "modules", "billing")
	submodule := filepath.Join(super, "libs", "billing")
	mustMkdirAll(t, modules)
	mustWriteFile(t, filepath.Join(modules, "HEAD"), "ref: refs/heads/billing-v2\n")
	mustMkdirAll(t, submodule)
	mustWriteFile(t, filepath.Join(submodule, ".git"), "gitdir: ../../.git/modules/billing\n")

	if got := mustFind(t, submodule).Branches().At(time.Now()); got != "billing-v2" {
		t.Fatalf("At() = %q, want billing-v2", got)
	}
}

func TestBranchesUnreadablePointerKnowsNothing(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, repo)
	mustWriteFile(t, filepath.Join(repo, ".git"), "not a pointer\n")

	if got := mustFind(t, repo).Branches().At(time.Now()); got != "" {
		t.Fatalf("At() = %q, want nothing", got)
	}
}

// The hand-written reflogs above are only as good as their assumptions about
// what git writes; this replays the same history with git itself.
func TestBranchesRealHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := realPath(t, t.TempDir())
	repo := filepath.Join(root, "payments-api")
	mustMkdirAll(t, repo)
	gitAt(t, repo, 1000, "init", "--quiet", "--initial-branch=main")
	mustWriteFile(t, filepath.Join(repo, "a.txt"), "a\n")
	gitAt(t, repo, 1000, "add", "a.txt")
	gitAt(t, repo, 1000, "commit", "--quiet", "-m", "a")
	gitAt(t, repo, 2000, "switch", "--quiet", "-c", "feature/login")
	gitAt(t, repo, 2100, "commit", "--quiet", "--allow-empty", "-m", "login")
	gitAt(t, repo, 3000, "switch", "--quiet", "main")
	gitAt(t, repo, 3100, "commit", "--quiet", "--allow-empty", "-m", "main moves on")
	gitAt(t, repo, 4000, "switch", "--quiet", "feature/login")
	gitAt(t, repo, 4100, "rebase", "--quiet", "main")
	gitAt(t, repo, 5000, "branch", "-m", "feature/login", "feature/sso")
	gitAt(t, repo, 6000, "checkout", "--quiet", "--detach", "HEAD")
	gitAt(t, repo, 7000, "switch", "--quiet", "-c", "hotfix")
	worktree := filepath.Join(repo, ".claude", "worktrees", "spike")
	gitAt(t, repo, 8000, "worktree", "add", "--quiet", "-b", "spike", worktree)

	branches := mustFind(t, repo).Branches()
	for _, c := range []struct {
		at   int64
		want string
	}{
		{1500, "main"},
		{2500, "feature/login"},
		{3500, "main"},
		{4100, "feature/login"}, // mid-rebase
		{5500, "feature/sso"},
		{6500, ""},
		{7500, "hotfix"},
		{9000, "hotfix"}, // a worktree's switches are its own
	} {
		if got := branches.At(time.Unix(c.at, 0)); got != c.want {
			t.Errorf("At(%d) = %q, want %q", c.at, got, c.want)
		}
	}
	if got := mustFind(t, worktree).Branches().At(time.Unix(9000, 0)); got != "spike" {
		t.Errorf("worktree At(9000) = %q, want spike", got)
	}
}

func checkoutWithHead(t *testing.T, head string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	mustWriteFile(t, filepath.Join(repo, ".git", "HEAD"), head+"\n")
	return repo
}

func writeReflog(t *testing.T, repo string, lines ...string) {
	t.Helper()
	mustMkdirAll(t, filepath.Join(repo, ".git", "logs"))
	mustWriteFile(t, filepath.Join(repo, ".git", "logs", "HEAD"), strings.Join(lines, ""))
}

func reflogLine(seconds int64, message string) string {
	return fmt.Sprintf("%s %s Dev Eloper <dev@example.com> %d +0900\t%s\n", commitA, commitB, seconds, message)
}

func mustFind(t *testing.T, dir string) Repo {
	t.Helper()
	repo, ok := Find(dir)
	if !ok {
		t.Fatalf("Find(%q) found nothing", dir)
	}
	return repo
}

// gitAt runs git with its clock set to seconds, which is what the reflog
// records for the change.
func gitAt(t *testing.T, dir string, seconds int64, args ...string) {
	t.Helper()
	date := fmt.Sprintf("@%d +0000", seconds)
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date, "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
