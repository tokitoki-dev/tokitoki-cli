// Package projectfile resolves explicit project identity files for usage
// events. It is intentionally independent from any editor so IDE heartbeats
// and local AI-agent scans use the same project naming rules.
package projectfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// Name is TokiToki's canonical per-project identity file.
	Name = ".tokitoki-project"
	// WakaTimeName is accepted as a migration-compatible fallback. This lets
	// an existing WakaTime project keep the same identity in TokiToki.
	WakaTimeName = ".wakatime-project"
	// Placeholder is replaced by the nearest VCS directory name, falling back
	// to the directory containing the project file.
	Placeholder = "{project}"

	maxProjectLineBytes = 4096
)

// Input contains the paths and existing identity supplied by an event source.
// Entity is searched first because it represents the file being worked on;
// ProjectPath is a fallback for out-of-tree entities such as AI plan files.
type Input struct {
	Entity      string
	ProjectPath string
	Branch      string
}

// Result contains the resolved identity. Found is reported separately by
// Resolve so callers can preserve their existing detection when no file is
// present.
type Result struct {
	Project     string
	ProjectPath string
	Branch      string
	Filepath    string
	Compatible  bool
}

// Resolve searches the entity and then the supplied project path for the
// nearest .tokitoki-project or .wakatime-project file. A nearer file wins; in
// the same directory, TokiToki's canonical file wins.
func Resolve(input Input) (Result, bool, error) {
	starts := []searchStart{
		{path: strings.TrimSpace(input.Entity), isFile: true},
		{path: strings.TrimSpace(input.ProjectPath), isFile: false},
	}

	var identityPath string
	var compatible bool
	var matchedStart searchStart
	for _, start := range starts {
		if start.path == "" || !filepath.IsAbs(start.path) {
			continue
		}
		var found bool
		var err error
		identityPath, compatible, found, err = find(start)
		if err != nil {
			return Result{}, false, err
		}
		if found {
			matchedStart = start
			break
		}
	}
	if identityPath == "" {
		return Result{}, false, nil
	}

	projectTemplate, branchOverride, err := read(identityPath)
	if err != nil {
		return Result{}, false, err
	}
	projectRoot := filepath.Dir(identityPath)
	project := projectTemplate
	if project == "" {
		project = projectName(projectRoot)
	}
	if strings.Contains(project, Placeholder) {
		base := detectVCSProject(matchedStart, projectRoot)
		project = strings.ReplaceAll(project, Placeholder, base)
	}
	project = strings.TrimSpace(project)
	if project == "" {
		project = projectName(projectRoot)
	}

	branch := strings.TrimSpace(input.Branch)
	if branchOverride != "" {
		branch = branchOverride
	}

	return Result{
		Project:     project,
		ProjectPath: projectRoot,
		Branch:      branch,
		Filepath:    identityPath,
		Compatible:  compatible,
	}, true, nil
}

type searchStart struct {
	path   string
	isFile bool
}

func find(start searchStart) (identityPath string, compatible, found bool, err error) {
	dir := filepath.Clean(start.path)
	if start.isFile {
		dir = filepath.Dir(dir)
	}

	for {
		for _, candidate := range []struct {
			name       string
			compatible bool
		}{
			{name: Name},
			{name: WakaTimeName, compatible: true},
		} {
			path := filepath.Join(dir, candidate.name)
			info, statErr := os.Stat(path)
			if statErr == nil && info.Mode().IsRegular() {
				return path, candidate.compatible, true, nil
			}
			// Anything else — the file is absent, the directory denies stat
			// (network mounts, tightened parents), or the name is a directory —
			// means "no identity file here". The walk covers every ancestor up
			// to the root, so treating an unreadable rung as empty can only
			// cost an override, never an event.
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, false, nil
		}
		dir = parent
	}
}

func read(path string) (project, branch string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open project identity file %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), maxProjectLineBytes)
	lines := make([]string, 0, 2)
	for len(lines) < 2 && scanner.Scan() {
		line := scanner.Text()
		if len(lines) == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if !utf8.ValidString(line) {
			return "", "", fmt.Errorf("project identity file %s is not valid UTF-8", path)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("read project identity file %s: %w", path, err)
	}
	if len(lines) > 0 {
		project = lines[0]
	}
	if len(lines) > 1 {
		branch = lines[1]
	}
	return project, branch, nil
}

func detectVCSProject(start searchStart, fallback string) string {
	if root, ok := findVCSRoot(start); ok {
		return projectName(root)
	}
	return projectName(fallback)
}

func findVCSRoot(start searchStart) (string, bool) {
	dir := filepath.Clean(start.path)
	if start.isFile {
		dir = filepath.Dir(dir)
	}
	for {
		for _, marker := range []string{".git", ".hg", ".svn"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func projectName(path string) string {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "unknown"
	}
	return name
}
