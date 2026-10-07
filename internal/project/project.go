// Package project decides which project a usage event belongs to. IDE
// heartbeats and AI agent scans both go through Resolve, so a folder is the
// same project whichever tool reported the work done in it.
package project

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/git"
	"github.com/tokitoki-dev/tokitoki-cli/internal/projectfile"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Input is what an event source knows about where the work happened.
type Input struct {
	// Entity is the file worked on. It is searched before ProjectPath: it says
	// where the work is more precisely than the folder a tool was opened in.
	Entity string
	// ProjectPath is the folder the source reported: an editor's workspace
	// folder, an agent's working directory.
	ProjectPath string
	// Project is a name the source insists on: heartbeat --project. Only an
	// identity file outranks it.
	//
	// Deprecated: kept only for editor plugins released before the CLI named
	// projects from repositories. They send their IDE project name this way
	// and read their figures back under that name, so it has to keep winning
	// for them; current plugins never set it. Delete it, and step 2 of
	// Resolve, once those plugin builds are gone.
	Project string
	// Fallback is the source's own label for the work, used only when nothing
	// on disk names the project: an agent's folder name, or a tool's own name
	// ("amp") when it records no folder at all.
	Fallback string
	// Branch is a branch the source claims: heartbeat --branch. It counts only
	// when the checkout's own record has no answer.
	Branch string
}

// Result is the project an event belongs to. Project is its identity — the
// server keys projects by name, so one repository is one project on every
// machine — and ProjectPath where it lives on this one.
type Result struct {
	Project     string
	ProjectPath string
	// Branches tells which branch the work was on at the time it was done.
	Branches Branches
	// GitRemote is the address of the git checkout's remote, set only when
	// ProjectPath is that checkout's root: uploaded paths are relative to
	// ProjectPath, and only then are they paths in the repository.
	GitRemote string
}

// Branches tells which branch the work was on. An identity file's branch
// line pins it; otherwise the checkout's own record decides, and what the
// source claims counts only when that record has no answer.
type Branches struct {
	pinned   string
	checkout git.Branches
	claimed  string
}

// At reports the branch the work done at t was on, or "" when nothing says.
func (b Branches) At(t time.Time) string {
	if b.pinned != "" {
		return b.pinned
	}
	if name := b.checkout.At(t); name != "" {
		return name
	}
	return b.claimed
}

// Resolve names the project. The first step that answers wins:
//
//  1. a .tokitoki identity file above Entity, then above ProjectPath
//  2. Project (deprecated, older editor plugins only)
//  3. the repository holding Entity, then ProjectPath
//  4. ProjectPath's folder
//  5. Fallback, else Unknown
//
// Steps 1 and 3 only search absolute paths. The Result is always usable: an
// error reports an identity file that could not be read, in which case
// identity files were ignored and the steps after them decided.
//
// The branch and the remote come from the git checkout the work is in, found
// in the same places, the nearest first — never from an agent's log: an
// agent records the branch of the folder it was started in, which need not be
// the checkout the work landed in.
func Resolve(in Input) (Result, error) {
	dirs := searchDirs(in)
	result, err := identify(in, dirs)

	repo, ok := workCheckout(dirs)
	result.Branches.checkout = repo.Branches()
	result.Branches.claimed = strings.TrimSpace(in.Branch)
	if ok && repo.Root == filepath.Clean(result.ProjectPath) {
		result.GitRemote = repo.Remote()
	}
	return result, err
}

// identify runs the naming steps Resolve lists.
func identify(in Input, dirs []string) (Result, error) {
	result, found, err := fromIdentityFile(dirs)
	if found {
		return result, nil
	}
	// Compatibility only — see Input.Project.
	if name := strings.TrimSpace(in.Project); name != "" {
		return named(name, in.ProjectPath), err
	}
	for _, dir := range dirs {
		if root, name, ok := repository(dir); ok {
			return Result{Project: name, ProjectPath: root}, err
		}
	}
	if path := absolute(in.ProjectPath); path != "" {
		if name := folderName(path); name != "" {
			return Result{Project: name, ProjectPath: path}, err
		}
	}
	return named(usage.NormalizeProject(in.Fallback), in.ProjectPath), err
}

// named files the work under a name the source gave, with the source's path
// exactly as it sent it — what this CLI always recorded for such a source.
func named(name, path string) Result {
	return Result{Project: name, ProjectPath: strings.TrimSpace(path)}
}

// workCheckout finds the git checkout the work is in: the nearest one
// holding any of dirs, most precise first.
func workCheckout(dirs []string) (git.Repo, bool) {
	for _, dir := range dirs {
		if repo, ok := git.Find(dir); ok && owns(repo.Root, dir) {
			return repo, true
		}
	}
	return git.Repo{}, false
}

// searchDirs lists where the work happened, most precise first.
func searchDirs(in Input) []string {
	dirs := make([]string, 0, 2)
	if entity := absolute(in.Entity); entity != "" {
		dirs = append(dirs, filepath.Dir(entity))
	}
	if path := absolute(in.ProjectPath); path != "" {
		dirs = append(dirs, path)
	}
	return dirs
}

func fromIdentityFile(dirs []string) (Result, bool, error) {
	for _, dir := range dirs {
		file, found, err := projectfile.Find(dir)
		if err != nil {
			return Result{}, false, err
		}
		if !found {
			continue
		}
		root := filepath.Dir(file.Path)
		return Result{Project: identityName(file.Project, dir, root), ProjectPath: root, Branches: Branches{pinned: file.Branch}}, true, nil
	}
	return Result{}, false, nil
}

// identityName expands an identity file's project line. An empty line, or
// one that expands to nothing, names the folder holding the file; the
// placeholder names the repository the work is in.
func identityName(line, dir, root string) string {
	fallback := folderName(root)
	if fallback == "" {
		fallback = usage.UnknownProject
	}
	if strings.Contains(line, projectfile.Placeholder) {
		repo := fallback
		if _, name, ok := repository(dir); ok {
			repo = name
		}
		line = strings.ReplaceAll(line, projectfile.Placeholder, repo)
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return fallback
}

// repository finds the checkout the work in dir belongs to.
func repository(dir string) (root, name string, ok bool) {
	root, name, ok = checkout(dir)
	if !ok || !owns(root, dir) {
		return "", "", false
	}
	return root, name, true
}

// owns reports whether the checkout at root counts for work in dir, which it
// holds. A checkout at the home directory is a dotfiles repository: it owns
// home itself (rel ".") and the dot-folders under it, never the ordinary
// folders — those are projects of their own, however its .git nests them.
func owns(root, dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || root != filepath.Clean(home) {
		return true
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && strings.HasPrefix(rel, ".")
}

// checkout finds the nearest checkout holding dir. Git knows its own layout —
// worktrees, submodules — so it answers first; Mercurial and Subversion are
// a marker folder at the checkout root.
func checkout(dir string) (root, name string, ok bool) {
	if repo, ok := git.Find(dir); ok {
		return repo.Root, repo.Name, true
	}
	for {
		if hasMarker(dir, ".hg", ".svn") {
			return dir, folderName(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

func hasMarker(dir string, markers ...string) bool {
	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func absolute(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

// folderName is a folder's name, or empty for a filesystem root.
func folderName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}
