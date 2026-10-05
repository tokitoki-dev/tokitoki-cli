// Package project decides which project a usage event belongs to. IDE
// heartbeats and AI agent scans both go through Resolve, so a folder is the
// same project whichever tool reported the work done in it.
package project

import (
	"os"
	"path/filepath"
	"strings"

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
	// identity file outranks it. Editors released before Tokitoki looked at
	// repositories send their own name this way and read their figures back
	// under that name, so it has to keep winning.
	Project string
	// AlternateProject is the source's own name for the project, used only
	// when nothing on disk names it: an agent's folder name, an editor's
	// heartbeat --alternate-project.
	AlternateProject string
	// Branch is the source's branch. An identity file can override it.
	Branch string
}

// Result is the project an event belongs to. Project is its identity — the
// server keys projects by name, so one repository is one project on every
// machine — and ProjectPath where it lives on this one.
type Result struct {
	Project     string
	ProjectPath string
	Branch      string
}

// Resolve names the project. The first step that answers wins:
//
//  1. a .tokitoki identity file above Entity, then above ProjectPath
//  2. Project
//  3. the repository holding Entity, then ProjectPath
//  4. ProjectPath's folder
//  5. AlternateProject, else Unknown
//
// Steps 1 and 3 only search absolute paths. The Result is always usable: an
// error reports an identity file that could not be read, in which case
// identity files were ignored and the steps after them decided.
func Resolve(in Input) (Result, error) {
	dirs := searchDirs(in)
	branch := strings.TrimSpace(in.Branch)

	result, found, err := fromIdentityFile(dirs, branch)
	if found {
		return result, nil
	}
	if name := strings.TrimSpace(in.Project); name != "" {
		return named(name, in.ProjectPath, branch), err
	}
	for _, dir := range dirs {
		if root, name, ok := repository(dir); ok {
			return Result{Project: name, ProjectPath: root, Branch: branch}, err
		}
	}
	if path := absolute(in.ProjectPath); path != "" {
		if name := folderName(path); name != "" {
			return Result{Project: name, ProjectPath: path, Branch: branch}, err
		}
	}
	return named(usage.NormalizeProject(in.AlternateProject), in.ProjectPath, branch), err
}

// named files the work under a name the source gave, with the source's path
// exactly as it sent it — what this CLI always recorded for such a source.
func named(name, path, branch string) Result {
	return Result{Project: name, ProjectPath: strings.TrimSpace(path), Branch: branch}
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

func fromIdentityFile(dirs []string, branch string) (Result, bool, error) {
	for _, dir := range dirs {
		file, found, err := projectfile.Find(dir)
		if err != nil {
			return Result{}, false, err
		}
		if !found {
			continue
		}
		root := filepath.Dir(file.Path)
		if file.Branch != "" {
			branch = file.Branch
		}
		return Result{Project: identityName(file.Project, dir, root), ProjectPath: root, Branch: branch}, true, nil
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
