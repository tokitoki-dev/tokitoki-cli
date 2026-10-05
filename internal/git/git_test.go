package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFindCheckoutFromNestedDirectory(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	nested := filepath.Join(repo, "apps", "web", "src")
	mustMkdirAll(t, nested)

	assertRepo(t, nested, Repo{Root: repo, Name: "payments-api"})
}

func TestFindCheckoutItself(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))

	assertRepo(t, repo, Repo{Root: repo, Name: "payments-api"})
}

// Events about deleted files, and agent cwds that were removed since, still
// belong to the repository around them.
func TestFindMissingDirectoryInsideCheckout(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))

	assertRepo(t, filepath.Join(repo, "deleted", "dir"), Repo{Root: repo, Name: "payments-api"})
}

func TestFindOutsideAnyCheckout(t *testing.T) {
	if repo, ok := Find(filepath.Join(t.TempDir(), "notes")); ok {
		t.Fatalf("Find() = %+v, want no repository", repo)
	}
}

// A folder holding several repositories is not one: the nearest .git wins.
func TestFindNearestCheckoutWins(t *testing.T) {
	outer := filepath.Join(t.TempDir(), "outer")
	inner := filepath.Join(outer, "vendor", "inner")
	mustMkdirAll(t, filepath.Join(outer, ".git"))
	mustMkdirAll(t, filepath.Join(inner, ".git"))

	assertRepo(t, filepath.Join(inner, "src"), Repo{Root: inner, Name: "inner"})
}

func TestFindLinkedWorktreeReportsMainCheckout(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "payments-api")
	worktree := filepath.Join(root, "payments-api-feature")
	linkWorktree(t, filepath.Join(repo, ".git"), worktree, "feature", false)

	assertRepo(t, filepath.Join(worktree, "src"), Repo{Root: repo, Name: "payments-api"})
}

// Claude Code puts its worktrees inside the repository. The worktree's own
// .git file is nearer than the repository's .git directory and must still
// resolve to the repository, not to the worktree's folder name.
func TestFindWorktreeInsideRepositoryReportsMainCheckout(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	worktree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	linkWorktree(t, filepath.Join(repo, ".git"), worktree, "fix-login", true)

	assertRepo(t, worktree, Repo{Root: repo, Name: "payments-api"})
}

func TestFindBareRepositoryWorktree(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "payments-api.git")
	worktree := filepath.Join(root, "main")
	linkWorktree(t, bare, worktree, "main", false)

	assertRepo(t, worktree, Repo{Root: bare, Name: "payments-api"})
}

func TestFindSubmoduleIsItsOwnRepository(t *testing.T) {
	super := filepath.Join(t.TempDir(), "platform")
	submodule := filepath.Join(super, "libs", "billing")
	modules := filepath.Join(super, ".git", "modules", "billing")
	mustMkdirAll(t, modules)
	mustWriteFile(t, filepath.Join(modules, "HEAD"), "ref: refs/heads/main\n")
	mustMkdirAll(t, submodule)
	mustWriteFile(t, filepath.Join(submodule, ".git"), "gitdir: ../../.git/modules/billing\n")

	assertRepo(t, filepath.Join(submodule, "src"), Repo{Root: submodule, Name: "billing"})
}

func TestFindUnreadablePointerStillMarksCheckout(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, repo)
	mustWriteFile(t, filepath.Join(repo, ".git"), "not a pointer\n")

	assertRepo(t, repo, Repo{Root: repo, Name: "payments-api"})
}

// The hand-built layouts above are only as good as their assumptions about
// what git writes; this checks them against the real thing.
func TestFindRealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := realPath(t, t.TempDir())
	repo := filepath.Join(root, "payments-api")
	worktree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	mustMkdirAll(t, repo)
	runGit(t, repo, "init", "--quiet")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "init")
	runGit(t, repo, "worktree", "add", "--quiet", worktree)

	assertRepo(t, worktree, Repo{Root: repo, Name: "payments-api"})
}

// linkWorktree writes what `git worktree add` leaves behind: a .git file in
// the worktree pointing at <common>/worktrees/<name>, whose commondir file
// points back at the shared git directory.
func linkWorktree(t *testing.T, common, worktree, name string, relative bool) {
	t.Helper()
	gitDir := filepath.Join(common, "worktrees", name)
	mustMkdirAll(t, gitDir)
	mustWriteFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/"+name+"\n")
	mustWriteFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	mustMkdirAll(t, worktree)
	pointer := gitDir
	if relative {
		rel, err := filepath.Rel(worktree, gitDir)
		if err != nil {
			t.Fatal(err)
		}
		pointer = rel
	}
	mustWriteFile(t, filepath.Join(worktree, ".git"), "gitdir: "+pointer+"\n")
}

func assertRepo(t *testing.T, dir string, want Repo) {
	t.Helper()
	got, ok := Find(dir)
	if !ok {
		t.Fatalf("Find(%q) found nothing, want %+v", dir, want)
	}
	if got != want {
		t.Fatalf("Find(%q) = %+v, want %+v", dir, got, want)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// realPath resolves the temp directory's symlinks (macOS /var is
// /private/var): git records the resolved path in its pointers.
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
