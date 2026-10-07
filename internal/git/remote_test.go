package git

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// A fork has its own origin and the repository it came from as another
// remote; branches are pushed to origin, so origin is the one to link to.
func TestRemoteOriginWinsWhereverItIsListed(t *testing.T) {
	repo := checkoutWithConfig(t, `[core]
	bare = false
[remote "upstream"]
	url = git@github.com:acme/payments-api.git
	fetch = +refs/heads/*:refs/remotes/upstream/*
[remote "origin"]
	url = git@github.com:dev/payments-api.git
`)

	assertRemote(t, repo, "git@github.com:dev/payments-api.git")
}

func TestRemoteWithoutOriginIsTheFirst(t *testing.T) {
	repo := checkoutWithConfig(t, `[remote "dzmm"]
	url = git@github.com:dzmm-paas2/podman.git
[remote "mirror"]
	url = https://gitlab.com/dzmm/podman.git
`)

	assertRemote(t, repo, "git@github.com:dzmm-paas2/podman.git")
}

func TestRemoteNoneConfigured(t *testing.T) {
	assertRemote(t, checkoutWithConfig(t, "[core]\n\tbare = false\n"), "")
	assertRemote(t, checkoutWithHead(t, "ref: refs/heads/main"), "")
}

// Config syntax git accepts: any case in section and key names, quoted
// values, trailing comments, and urls outside a remote that are not one.
func TestRemoteReadsConfigAsGitDoes(t *testing.T) {
	repo := checkoutWithConfig(t, `[url "git@github.com:"]
	insteadOf = https://github.com/
[Remote "origin"] ; the main one
	URL = "git@github.com:acme/payments-api.git" # quoted
	url = git@github.com:acme/second-url.git
`)

	assertRemote(t, repo, "git@github.com:acme/payments-api.git")
}

func TestRemoteNeverCarriesCredentials(t *testing.T) {
	for raw, want := range map[string]string{
		"https://x-access-token:ghp_secret@github.com/acme/api.git":  "https://github.com/acme/api.git",
		"https://ghp_secret@github.com/acme/api":                     "https://github.com/acme/api",
		"https://oauth2:glpat-secret@git.corp.com:8443/team/api.git": "https://git.corp.com:8443/team/api.git",
		"ssh://git@git.corp.com:2222/team/api.git":                   "ssh://git.corp.com:2222/team/api.git",
		"git@github.com:acme/api.git":                                "git@github.com:acme/api.git",
		"github.com:acme/api.git":                                    "github.com:acme/api.git",
	} {
		if got := publicAddress(raw); got != want {
			t.Errorf("publicAddress(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A remote that is a path on this machine has no page to link to, and
// uploading it would publish this machine's layout.
func TestRemoteOnThisMachineIsNotSent(t *testing.T) {
	for _, raw := range []string{
		"/srv/git/payments-api.git",
		"../payments-api",
		"file:///srv/git/payments-api.git",
		`C:\repos\payments-api`,
		"C:/repos/payments-api",
		`\\server\share\payments-api.git`,
		"",
	} {
		if got := publicAddress(raw); got != "" {
			t.Errorf("publicAddress(%q) = %q, want nothing", raw, got)
		}
	}
}

// A worktree shares its repository's config; a submodule has its own.
func TestRemoteOfWorktreeAndSubmodule(t *testing.T) {
	repo := checkoutWithConfig(t, "[remote \"origin\"]\n\turl = git@github.com:acme/platform.git\n")
	worktree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	linkWorktree(t, filepath.Join(repo, ".git"), worktree, "fix-login", true)
	assertRemote(t, worktree, "git@github.com:acme/platform.git")

	modules := filepath.Join(repo, ".git", "modules", "billing")
	submodule := filepath.Join(repo, "libs", "billing")
	mustMkdirAll(t, modules)
	mustWriteFile(t, filepath.Join(modules, "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/billing.git\n")
	mustMkdirAll(t, submodule)
	mustWriteFile(t, filepath.Join(submodule, ".git"), "gitdir: ../../.git/modules/billing\n")
	assertRemote(t, submodule, "git@github.com:acme/billing.git")
}

func TestRemoteRealRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := filepath.Join(realPath(t, t.TempDir()), "payments-api")
	mustMkdirAll(t, repo)
	runGit(t, repo, "init", "--quiet")
	runGit(t, repo, "remote", "add", "upstream", "git@github.com:acme/payments-api.git")
	runGit(t, repo, "remote", "add", "origin", "https://dev:ghp_secret@github.com/dev/payments-api.git")

	assertRemote(t, repo, "https://github.com/dev/payments-api.git")
}

func checkoutWithConfig(t *testing.T, config string) string {
	t.Helper()
	repo := checkoutWithHead(t, "ref: refs/heads/main")
	mustWriteFile(t, filepath.Join(repo, ".git", "config"), config)
	return repo
}

func assertRemote(t *testing.T, dir, want string) {
	t.Helper()
	if got := mustFind(t, dir).Remote(); got != want {
		t.Fatalf("Remote() in %q = %q, want %q", dir, got, want)
	}
}
