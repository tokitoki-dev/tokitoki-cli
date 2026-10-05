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

// Repo is the git repository a directory belongs to.
type Repo struct {
	// Root is the repository's working tree. A linked worktree (git worktree
	// add, Claude Code's .claude/worktrees) reports its main checkout, so all
	// worktrees of one repository are one repository.
	Root string
	// Name is the repository's name: the main checkout's folder name.
	Name string
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
// back to the repository, and a submodule's to <super>/.git/modules/<name>,
// which does not — a submodule is a repository of its own. A pointer that
// cannot be followed still marks dir as a checkout; git put it there.
func open(dir string) (Repo, bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return Repo{}, false
	}
	if !info.IsDir() {
		if common, ok := commonDir(dotGit); ok {
			return mainCheckout(common), true
		}
	}
	return Repo{Root: dir, Name: filepath.Base(dir)}, true
}

// commonDir follows a .git file to a linked worktree's shared git directory.
func commonDir(dotGit string) (string, bool) {
	line, ok := firstLine(dotGit)
	if !ok {
		return "", false
	}
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = resolve(filepath.Dir(dotGit), strings.TrimSpace(gitDir))

	common, ok := firstLine(filepath.Join(gitDir, "commondir"))
	if !ok || common == "" {
		return "", false
	}
	return resolve(gitDir, common), true
}

// mainCheckout names the repository behind a shared git directory: the
// checkout holding it when it is a .git folder, and the directory itself,
// less a .git suffix, for a bare repository.
func mainCheckout(common string) Repo {
	if filepath.Base(common) == ".git" {
		root := filepath.Dir(common)
		return Repo{Root: root, Name: filepath.Base(root)}
	}
	return Repo{Root: common, Name: strings.TrimSuffix(filepath.Base(common), ".git")}
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
