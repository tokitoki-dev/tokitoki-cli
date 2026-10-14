// Package projectfile finds and reads Tokitoki's per-project identity file.
// It only knows the file; deciding what an event's project is, with or
// without one, is internal/project's job.
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
	// Name is Tokitoki's canonical per-project configuration file, placed in
	// the project root. The shared data directory ~/.tokitoki shares the name
	// but is a directory; the lookup only accepts regular files, so the two
	// never collide.
	Name = ".tokitoki"
	// Placeholder in the project line stands for the name of the repository
	// the work is in, falling back to the folder holding the file.
	Placeholder = "{project}"

	maxProjectLineBytes = 4096
)

// File is an identity file as written.
type File struct {
	// Path is the file itself; the folder holding it is the project root.
	Path string
	// Project is line 1: a name, a template holding Placeholder, or empty.
	Project string
	// Branch is line 2: a branch override, or empty.
	Branch string
}

// Find returns the nearest identity file in dir or its parents. An error
// means the nearest file exists but cannot be read.
func Find(dir string) (File, bool, error) {
	dir = filepath.Clean(dir)
	for {
		path := filepath.Join(dir, Name)
		info, statErr := os.Stat(path)
		if statErr == nil && info.Mode().IsRegular() {
			file, err := read(path)
			return file, err == nil, err
		}
		// Anything else — the file is absent, the directory denies stat
		// (network mounts, tightened parents), or the name is a directory
		// (the shared data directory ~/.tokitoki) — means "no project file
		// here". The walk covers every ancestor up to the root, so treating
		// an unreadable rung as empty can only cost an override, never an
		// event.

		parent := filepath.Dir(dir)
		if parent == dir {
			return File{}, false, nil
		}
		dir = parent
	}
}

func read(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("open project identity file %s: %w", path, err)
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
			return File{}, fmt.Errorf("project identity file %s is not valid UTF-8", path)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if err := scanner.Err(); err != nil {
		return File{}, fmt.Errorf("read project identity file %s: %w", path, err)
	}

	result := File{Path: path}
	if len(lines) > 0 {
		result.Project = lines[0]
	}
	if len(lines) > 1 {
		result.Branch = lines[1]
	}
	return result, nil
}
