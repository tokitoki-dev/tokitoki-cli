package projectfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProjectAndBranch(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, "customer-portal\nrelease/2026\nignored\n")

	result, found, err := Resolve(Input{
		Entity:      entity,
		ProjectPath: filepath.Join(projectDir, "wrong-root"),
		Branch:      "wrong-branch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	if result.Project != "customer-portal" {
		t.Fatalf("project = %q, want customer-portal", result.Project)
	}
	if result.Branch != "release/2026" {
		t.Fatalf("branch = %q, want release/2026", result.Branch)
	}
	if result.ProjectPath != projectDir {
		t.Fatalf("project path = %q, want %q", result.ProjectPath, projectDir)
	}
	if result.Filepath != filepath.Join(projectDir, Name) {
		t.Fatalf("identity file = %q", result.Filepath)
	}
	if result.Compatible {
		t.Fatal("canonical file reported as compatible fallback")
	}
}

func TestResolveEmptyFileUsesContainingFolderAndKeepsBranch(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, "")

	result, found, err := Resolve(Input{Entity: entity, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	if result.Project != filepath.Base(projectDir) {
		t.Fatalf("project = %q, want folder name %q", result.Project, filepath.Base(projectDir))
	}
	if result.Branch != "main" {
		t.Fatalf("branch = %q, want existing branch", result.Branch)
	}
}

func TestResolvePlaceholderUsesNestedVCSProject(t *testing.T) {
	root := t.TempDir()
	companyDir := filepath.Join(root, "my-company")
	repoDir := filepath.Join(companyDir, "payments-api")
	entity := filepath.Join(repoDir, "src", "main.go")
	mustMkdirAll(t, filepath.Dir(entity))
	mustMkdirAll(t, filepath.Join(repoDir, ".git"))
	mustWriteFile(t, entity, "package main\n")
	writeProjectFile(t, companyDir, Name, "my-company/{project}\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	if result.Project != "my-company/payments-api" {
		t.Fatalf("project = %q, want my-company/payments-api", result.Project)
	}
	if result.ProjectPath != companyDir {
		t.Fatalf("project path = %q, want marker directory %q", result.ProjectPath, companyDir)
	}
}

func TestResolvePlaceholderWithoutVCSUsesMarkerFolder(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, "team/{project}/{project}\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Resolve() found = false, want true")
	}
	want := "team/" + filepath.Base(projectDir) + "/" + filepath.Base(projectDir)
	if result.Project != want {
		t.Fatalf("project = %q, want %q", result.Project, want)
	}
}

func TestResolveEntityTakesPrecedenceOverProjectPath(t *testing.T) {
	root := t.TempDir()
	entityProject := filepath.Join(root, "entity-project")
	providedProject := filepath.Join(root, "provided-project")
	entity := filepath.Join(entityProject, "main.go")
	mustMkdirAll(t, entityProject)
	mustMkdirAll(t, providedProject)
	mustWriteFile(t, entity, "package main\n")
	writeProjectFile(t, entityProject, Name, "from-entity\n")
	writeProjectFile(t, providedProject, Name, "from-project-path\n")

	result, found, err := Resolve(Input{Entity: entity, ProjectPath: providedProject})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "from-entity" {
		t.Fatalf("Resolve() = (%+v, %t), want entity project", result, found)
	}
}

func TestResolveUsesProjectPathForOutOfTreeEntity(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "real-project")
	entityDir := filepath.Join(root, "agent-plans")
	entity := filepath.Join(entityDir, "plan.md")
	mustMkdirAll(t, projectDir)
	mustMkdirAll(t, entityDir)
	mustWriteFile(t, entity, "plan\n")
	writeProjectFile(t, projectDir, Name, "shared-project-name\n")

	result, found, err := Resolve(Input{Entity: entity, ProjectPath: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "shared-project-name" || result.ProjectPath != projectDir {
		t.Fatalf("Resolve() = (%+v, %t), want project-path identity", result, found)
	}
}

func TestResolvePlaceholderUsesProjectPathVCSForOutOfTreeEntity(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "real-project")
	entityRepo := filepath.Join(root, "unrelated-agent-repo")
	entity := filepath.Join(entityRepo, "plan.md")
	mustMkdirAll(t, filepath.Join(projectDir, ".git"))
	mustMkdirAll(t, filepath.Join(entityRepo, ".git"))
	mustWriteFile(t, entity, "plan\n")
	writeProjectFile(t, projectDir, Name, "team/{project}\n")

	result, found, err := Resolve(Input{Entity: entity, ProjectPath: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "team/real-project" {
		t.Fatalf("Resolve() = (%+v, %t), want project-path VCS identity", result, found)
	}
}

func TestResolveAcceptsWakaTimeProjectAsNearestCompatibilityFile(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	inner := filepath.Join(outer, "inner")
	entity := filepath.Join(inner, "main.go")
	mustMkdirAll(t, inner)
	mustWriteFile(t, entity, "package main\n")
	writeProjectFile(t, outer, Name, "outer-tokitoki\n")
	writeProjectFile(t, inner, WakaTimeName, "inner-wakatime\nlegacy-branch\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "inner-wakatime" || result.Branch != "legacy-branch" {
		t.Fatalf("Resolve() = (%+v, %t), want nearest WakaTime identity", result, found)
	}
	if !result.Compatible {
		t.Fatal("WakaTime file not reported as compatibility fallback")
	}
}

func TestResolveCanonicalWinsInSameDirectory(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, "tokitoki-name\n")
	writeProjectFile(t, projectDir, WakaTimeName, "wakatime-name\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "tokitoki-name" || result.Compatible {
		t.Fatalf("Resolve() = (%+v, %t), want canonical identity", result, found)
	}
}

func TestResolveSupportsUTF8BOMAndCRLF(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, "\ufeff  日本語プロジェクト  \r\n  feature/name  \r\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "日本語プロジェクト" || result.Branch != "feature/name" {
		t.Fatalf("Resolve() = (%+v, %t), want trimmed UTF-8 values", result, found)
	}
}

func TestResolveNoFilePreservesExistingDetection(t *testing.T) {
	_, entity := projectTree(t)
	result, found, err := Resolve(Input{Entity: entity, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if found || result != (Result{}) {
		t.Fatalf("Resolve() = (%+v, %t), want no result", result, found)
	}
}

func TestResolveDoesNotTreatDotWakatimeConfigAsProjectIdentity(t *testing.T) {
	projectDir, entity := projectTree(t)
	mustWriteFile(t, filepath.Join(projectDir, ".wakatime"), "[settings]\nproject=not-an-identity\n")

	result, found, err := Resolve(Input{Entity: entity, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if found || result != (Result{}) {
		t.Fatalf("Resolve() = (%+v, %t), want .wakatime ignored", result, found)
	}
}

// A directory that happens to carry the identity file's name is not an
// identity file. It must be skipped — never an error that stops the event —
// and a real identity file further up the tree must still be honored.
func TestResolveSkipsDirectoryAtIdentityPath(t *testing.T) {
	projectDir, entity := projectTree(t)
	mustMkdirAll(t, filepath.Join(projectDir, Name))
	writeProjectFile(t, filepath.Dir(projectDir), Name, "parent-name\n")

	result, found, err := Resolve(Input{Entity: entity})
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Project != "parent-name" {
		t.Fatalf("Resolve() = (%+v, %t), want parent identity file to win", result, found)
	}
}

func TestResolveRejectsOversizedFirstLine(t *testing.T) {
	projectDir, entity := projectTree(t)
	writeProjectFile(t, projectDir, Name, strings.Repeat("x", maxProjectLineBytes+1)+"\n")

	_, _, err := Resolve(Input{Entity: entity})
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("Resolve() error = %v, want oversized-line error", err)
	}
}

func projectTree(t *testing.T) (string, string) {
	t.Helper()
	projectDir := filepath.Join(t.TempDir(), "sample-project")
	entity := filepath.Join(projectDir, "src", "main.go")
	mustMkdirAll(t, filepath.Dir(entity))
	mustWriteFile(t, entity, "package main\n")
	return projectDir, entity
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
