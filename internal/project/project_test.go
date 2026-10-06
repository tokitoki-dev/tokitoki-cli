package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokitoki-dev/tokitoki-cli/internal/projectfile"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// An agent that cd'd into a subfolder still works in the repository: the
// subfolder's name is not a project.
func TestResolveSubfolderOfRepositoryIsTheRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "cps-dev")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	cwd := filepath.Join(repo, "cps-web", "apps", "web", "src")
	mustMkdirAll(t, cwd)

	assertResult(t, Input{ProjectPath: cwd, Fallback: "src", Branch: "main"},
		Result{Project: "cps-dev", ProjectPath: repo, Branch: "main"})
}

// A folder holding several repositories is opened, and a file in one of them
// is edited: the file says which project the work is in.
func TestResolveEntityRepositoryWinsOverFolder(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "tracklm")
	repo := filepath.Join(workspace, "tokitoki-cli")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	entity := filepath.Join(repo, "cmd", "main.go")

	assertResult(t, Input{Entity: entity, ProjectPath: workspace, Fallback: "tracklm"},
		Result{Project: "tokitoki-cli", ProjectPath: repo})
}

// An entity outside any repository — an agent's plan file — leaves the
// decision to the folder the work was reported in.
func TestResolveOutOfTreeEntityFallsBackToFolderRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	plan := filepath.Join(root, "agent-plans", "plan.md")

	assertResult(t, Input{Entity: plan, ProjectPath: filepath.Join(repo, "src")},
		Result{Project: "payments-api", ProjectPath: repo})
}

func TestResolveWorktreeIsItsRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	gitDir := filepath.Join(repo, ".git", "worktrees", "fix-login")
	worktree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	mustMkdirAll(t, gitDir)
	mustWriteFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	mustMkdirAll(t, worktree)
	mustWriteFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")

	assertResult(t, Input{ProjectPath: worktree},
		Result{Project: "payments-api", ProjectPath: repo})
}

func TestResolveMercurialAndSubversionCheckouts(t *testing.T) {
	for _, marker := range []string{".hg", ".svn"} {
		t.Run(marker, func(t *testing.T) {
			checkout := filepath.Join(t.TempDir(), "legacy-app")
			mustMkdirAll(t, filepath.Join(checkout, marker))

			assertResult(t, Input{ProjectPath: filepath.Join(checkout, "src")},
				Result{Project: "legacy-app", ProjectPath: checkout})
		})
	}
}

func TestResolveFolderOutsideRepositoryIsItsOwnProject(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "notes")

	assertResult(t, Input{ProjectPath: folder + string(filepath.Separator), Fallback: "My Notes"},
		Result{Project: "notes", ProjectPath: folder})
}

// A home directory under git is a dotfiles repository. Work in ordinary
// folders under home is not work on it, even though its .git nests them.
func TestResolveHomeRepositoryDoesNotSwallowFoldersUnderHome(t *testing.T) {
	home := homeRepository(t)
	workspace := filepath.Join(home, "workspace", "tracklm")

	assertResult(t, Input{ProjectPath: workspace},
		Result{Project: "tracklm", ProjectPath: workspace})
	assertResult(t, Input{Entity: filepath.Join(home, "Documents", "plan.md"), ProjectPath: workspace},
		Result{Project: "tracklm", ProjectPath: workspace})
}

// What a dotfiles repository does own: home itself and its dot-folders.
func TestResolveHomeRepositoryOwnsHomeAndDotFolders(t *testing.T) {
	home := homeRepository(t)
	want := Result{Project: filepath.Base(home), ProjectPath: home}

	assertResult(t, Input{ProjectPath: home}, want)
	assertResult(t, Input{ProjectPath: filepath.Join(home, ".config", "nvim")}, want)
	assertResult(t, Input{Entity: filepath.Join(home, ".zshrc"), ProjectPath: filepath.Join(home, "workspace")}, want)
}

// Repositories under home are found before home's own .git ever is.
func TestResolveRepositoryUnderHomeRepository(t *testing.T) {
	home := homeRepository(t)
	repo := filepath.Join(home, "src", "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))

	assertResult(t, Input{ProjectPath: filepath.Join(repo, "cmd")},
		Result{Project: "payments-api", ProjectPath: repo})
}

func homeRepository(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "eren")
	mustMkdirAll(t, filepath.Join(home, ".git"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// Sources with no folder at all keep both their name and their path as
// given: the server keys the project by that path, so rewriting it would
// split the project's history.
func TestResolveSourceWithoutFolderKeepsItsOwnIdentity(t *testing.T) {
	assertResult(t, Input{ProjectPath: "Amp", Fallback: "amp"},
		Result{Project: "amp", ProjectPath: "Amp"})
}

// Editors released before repositories counted send their own name as
// Project and read their status bar figures back under it. Whatever the disk
// says, that name and the folder exactly as sent must stand.
func TestResolveExplicitProjectWinsOverRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "cps-dev")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	folder := filepath.Join(repo, "apps", "web") + string(filepath.Separator)

	assertResult(t, Input{Entity: filepath.Join(folder, "page.tsx"), ProjectPath: folder, Project: "Web App", Fallback: "web", Branch: "main"},
		Result{Project: "Web App", ProjectPath: folder, Branch: "main"})
}

func TestResolveIdentityFileWinsOverExplicitProject(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "sample-project")
	writeProjectFile(t, projectDir, "pinned-name\n")

	assertResult(t, Input{Entity: filepath.Join(projectDir, "main.go"), ProjectPath: projectDir, Project: "ide-name"},
		Result{Project: "pinned-name", ProjectPath: projectDir})
}

func TestResolveNothingKnownIsUnknown(t *testing.T) {
	assertResult(t, Input{Entity: "relative/main.go"},
		Result{Project: usage.UnknownProject})
}

func TestResolveIdentityFileWinsOverRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	writeProjectFile(t, repo, "customer-portal\nrelease/2026\n")

	assertResult(t, Input{Entity: filepath.Join(repo, "src", "main.go"), ProjectPath: repo, Branch: "wrong-branch"},
		Result{Project: "customer-portal", ProjectPath: repo, Branch: "release/2026"})
}

func TestResolveEmptyIdentityFileNamesItsFolderAndKeepsBranch(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "sample-project")
	writeProjectFile(t, projectDir, "")

	assertResult(t, Input{Entity: filepath.Join(projectDir, "src", "main.go"), Branch: "main"},
		Result{Project: "sample-project", ProjectPath: projectDir, Branch: "main"})
}

func TestResolvePlaceholderUsesNestedRepository(t *testing.T) {
	companyDir := filepath.Join(t.TempDir(), "my-company")
	repo := filepath.Join(companyDir, "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	writeProjectFile(t, companyDir, "my-company/"+projectfile.Placeholder+"\n")

	assertResult(t, Input{Entity: filepath.Join(repo, "src", "main.go")},
		Result{Project: "my-company/payments-api", ProjectPath: companyDir})
}

func TestResolvePlaceholderWithoutRepositoryUsesIdentityFolder(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "sample-project")
	writeProjectFile(t, projectDir, "team/{project}/{project}\n")

	assertResult(t, Input{Entity: filepath.Join(projectDir, "src", "main.go")},
		Result{Project: "team/sample-project/sample-project", ProjectPath: projectDir})
}

func TestResolveEntityIdentityFileTakesPrecedenceOverProjectPath(t *testing.T) {
	root := t.TempDir()
	entityProject := filepath.Join(root, "entity-project")
	providedProject := filepath.Join(root, "provided-project")
	writeProjectFile(t, entityProject, "from-entity\n")
	writeProjectFile(t, providedProject, "from-project-path\n")

	assertResult(t, Input{Entity: filepath.Join(entityProject, "main.go"), ProjectPath: providedProject},
		Result{Project: "from-entity", ProjectPath: entityProject})
}

func TestResolveProjectPathIdentityFileForOutOfTreeEntity(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "real-project")
	writeProjectFile(t, projectDir, "shared-project-name\n")

	assertResult(t, Input{Entity: filepath.Join(root, "agent-plans", "plan.md"), ProjectPath: projectDir},
		Result{Project: "shared-project-name", ProjectPath: projectDir})
}

// The placeholder names the repository of the place the identity file was
// found from — not that of an unrelated entity searched before it.
func TestResolvePlaceholderUsesProjectPathRepositoryForOutOfTreeEntity(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "real-project")
	entityRepo := filepath.Join(root, "unrelated-agent-repo")
	mustMkdirAll(t, filepath.Join(projectDir, ".git"))
	mustMkdirAll(t, filepath.Join(entityRepo, ".git"))
	writeProjectFile(t, projectDir, "team/{project}\n")

	// Step 1 searches both places for identity files before step 2 looks for
	// any repository, so the entity's repository does not get a say here.
	assertResult(t, Input{Entity: filepath.Join(entityRepo, "plan.md"), ProjectPath: projectDir},
		Result{Project: "team/real-project", ProjectPath: projectDir})
}

// A broken identity file must never cost the event: it is reported and the
// repository decides as if the file were not there.
func TestResolveUnreadableIdentityFileFallsBackToRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	writeProjectFile(t, repo, strings.Repeat("x", 4097)+"\n")

	got, err := Resolve(Input{ProjectPath: repo})
	if err == nil {
		t.Fatal("Resolve() error = nil, want the unreadable identity file reported")
	}
	if want := (Result{Project: "payments-api", ProjectPath: repo}); got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}
}

func assertResult(t *testing.T, input Input, want Result) {
	t.Helper()
	got, err := Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve(%+v) = %+v, want %+v", input, got, want)
	}
}

func writeProjectFile(t *testing.T, dir, contents string) {
	t.Helper()
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, projectfile.Name), contents)
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
