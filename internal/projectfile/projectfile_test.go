package projectfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindReadsProjectAndBranch(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, "customer-portal\nrelease/2026\nignored\n")

	file := mustFind(t, entityDir)
	want := File{Path: filepath.Join(projectDir, Name), Project: "customer-portal", Branch: "release/2026"}
	if file != want {
		t.Fatalf("Find() = %+v, want %+v", file, want)
	}
}

func TestFindKeepsEmptyLinesEmpty(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, "")

	file := mustFind(t, entityDir)
	if file.Project != "" || file.Branch != "" {
		t.Fatalf("Find() = %+v, want empty project and branch", file)
	}
}

func TestFindKeepsPlaceholderForCaller(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, "team/"+Placeholder+"\n")

	if file := mustFind(t, entityDir); file.Project != "team/"+Placeholder {
		t.Fatalf("project = %q, want the template as written", file.Project)
	}
}

func TestFindNearestFileWins(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, filepath.Dir(projectDir), Name, "outer\n")
	writeProjectFile(t, projectDir, Name, "inner\n")

	if file := mustFind(t, entityDir); file.Project != "inner" {
		t.Fatalf("project = %q, want the nearest file", file.Project)
	}
}

func TestFindNothing(t *testing.T) {
	_, entityDir := projectTree(t)
	file, found, err := Find(entityDir)
	if err != nil || found || file != (File{}) {
		t.Fatalf("Find() = (%+v, %t, %v), want nothing", file, found, err)
	}
}

func TestFindIgnoresOtherToolsProjectFiles(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	inner := filepath.Join(outer, "inner")
	mustMkdirAll(t, inner)
	writeProjectFile(t, outer, Name, "outer-tokitoki\n")
	writeProjectFile(t, inner, ".legacy-project", "inner-legacy\nlegacy-branch\n")
	mustWriteFile(t, filepath.Join(inner, ".toolconfig"), "[settings]\nproject=not-an-identity\n")

	if file := mustFind(t, inner); file.Project != "outer-tokitoki" {
		t.Fatalf("project = %q, want other dotfiles ignored", file.Project)
	}
}

func TestFindSupportsUTF8BOMAndCRLF(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, "\ufeff  日本語プロジェクト  \r\n  feature/name  \r\n")

	file := mustFind(t, entityDir)
	if file.Project != "日本語プロジェクト" || file.Branch != "feature/name" {
		t.Fatalf("Find() = %+v, want trimmed UTF-8 values", file)
	}
}

// A directory that happens to carry the identity file's name is not an
// identity file. It must be skipped — never an error that stops the event —
// and a real identity file further up the tree must still be honored.
func TestFindSkipsDirectoryAtIdentityPath(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	mustMkdirAll(t, filepath.Join(projectDir, Name))
	writeProjectFile(t, filepath.Dir(projectDir), Name, "parent-name\n")

	if file := mustFind(t, entityDir); file.Project != "parent-name" {
		t.Fatalf("project = %q, want parent identity file to win", file.Project)
	}
}

func TestFindRejectsOversizedFirstLine(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, strings.Repeat("x", maxProjectLineBytes+1)+"\n")

	_, found, err := Find(entityDir)
	if found || err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("Find() = (found %t, %v), want oversized-line error", found, err)
	}
}

func TestFindRejectsInvalidUTF8(t *testing.T) {
	projectDir, entityDir := projectTree(t)
	writeProjectFile(t, projectDir, Name, "bad\xffname\n")

	_, found, err := Find(entityDir)
	if found || err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("Find() = (found %t, %v), want UTF-8 error", found, err)
	}
}

func mustFind(t *testing.T, dir string) File {
	t.Helper()
	file, found, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("Find(%q) found nothing", dir)
	}
	return file
}

// projectTree returns a project folder and a source folder inside it.
func projectTree(t *testing.T) (string, string) {
	t.Helper()
	projectDir := filepath.Join(t.TempDir(), "sample-project")
	entityDir := filepath.Join(projectDir, "src")
	mustMkdirAll(t, entityDir)
	return projectDir, entityDir
}

func writeProjectFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, name), contents)
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
