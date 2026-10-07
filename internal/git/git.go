// Package git answers questions about git checkouts by reading .git on disk.
// It never runs the git binary: the CLI must work on machines without one,
// and a process per event would cost more than the event.
package git

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxPointerBytes bounds what is read from a .git or commondir file. Both are
// one short line; anything longer is not a pointer git wrote.
const maxPointerBytes = 4096

// Repo is the git checkout a directory belongs to.
type Repo struct {
	// Root is the checkout holding the directory — for a linked worktree (git
	// worktree add, Claude Code's .claude/worktrees) the worktree's own
	// folder, not the main checkout: the files worked on are under it.
	Root string
	// Name is the repository's name, the same from every worktree of it, so
	// all of them are one project.
	Name string
	// gitDir is the checkout's own git directory, holding its HEAD and the
	// reflog of HEAD: .git itself, or where a worktree's or submodule's .git
	// file points. Empty when that pointer cannot be followed.
	gitDir string
	// commonDir is the git directory the checkout shares with its repository,
	// holding the config: a linked worktree's main .git, else gitDir itself.
	commonDir string
}

// Find reports the repository dir is in, searching dir and then its parents
// for the nearest .git. dir should be absolute. It does not have to exist:
// a deleted file still belongs to the repository it was deleted from.
func Find(dir string) (Repo, bool) {
	dir = filepath.Clean(dir)
	for {
		if repo, ok := open(dir); ok {
			return repo, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Repo{}, false
		}
		dir = parent
	}
}

// open reports the repository whose .git entry lives directly in dir.
//
// A .git directory is an ordinary checkout. A .git file points elsewhere:
// a linked worktree's to <common>/worktrees/<name>, which carries a commondir
// back to the repository whose name the worktree takes, and a submodule's to
// <super>/.git/modules/<name>, which does not — a submodule is a repository
// of its own. Either way dir is the checkout, and a pointer that cannot be
// followed still marks it as one; git put it there.
func open(dir string) (Repo, bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return Repo{}, false
	}
	repo := Repo{Root: dir, Name: filepath.Base(dir), gitDir: dotGit, commonDir: dotGit}
	if !info.IsDir() {
		repo.gitDir = pointedDir(dotGit)
		repo.commonDir = repo.gitDir
		if common, ok := commonDir(repo.gitDir); ok {
			repo.Name = repoName(common)
			repo.commonDir = common
		}
	}
	return repo, true
}

// pointedDir follows a .git file to the git directory it names, or "" when
// it names none.
func pointedDir(dotGit string) string {
	line, ok := firstLine(dotGit)
	if !ok {
		return ""
	}
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	return resolve(filepath.Dir(dotGit), strings.TrimSpace(gitDir))
}

// commonDir follows a linked worktree's git directory to the one it shares
// with its repository. A submodule's has no commondir: it shares nothing.
func commonDir(gitDir string) (string, bool) {
	if gitDir == "" {
		return "", false
	}
	common, ok := firstLine(filepath.Join(gitDir, "commondir"))
	if !ok || common == "" {
		return "", false
	}
	return resolve(gitDir, common), true
}

// repoName names a repository after its shared git directory: repo.git is
// "repo", and a directory left without a name once .git is dropped — the .git
// of a main checkout, or the .bare of a bare clone kept beside its worktrees —
// is named after the folder holding it.
func repoName(common string) string {
	name := strings.TrimSuffix(filepath.Base(common), ".git")
	if name == "" || strings.HasPrefix(name, ".") {
		return filepath.Base(filepath.Dir(common))
	}
	return name
}

func resolve(base, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

func firstLine(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxPointerBytes))
	if !scanner.Scan() {
		return "", false
	}
	return strings.TrimSpace(scanner.Text()), true
}
