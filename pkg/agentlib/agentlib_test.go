package agentlib

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
)

func TestNewUsesDefaultDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, config.DataDirName)
	if client.DataDir() != want {
		t.Fatalf("DataDir() = %q, want %q", client.DataDir(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestSetAndGetAPIKey(t *testing.T) {
	client := newTestClient(t)

	if err := client.SetAPIKey("  tokitoki_test_key \n"); err != nil {
		t.Fatal(err)
	}

	apiKey, err := client.GetAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "tokitoki_test_key" {
		t.Fatalf("GetAPIKey() = %q, want saved key", apiKey)
	}
}

func TestGetAPIKeyReturnsMissingError(t *testing.T) {
	client := newTestClient(t)

	_, err := client.GetAPIKey()
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("GetAPIKey() error = %v, want ErrMissingAPIKey", err)
	}
}

func TestSyncRejectsEmptyDirectories(t *testing.T) {
	client := newTestClient(t)

	err := client.Sync(context.Background(), SyncOptions{})
	if !errors.Is(err, ErrNoScanDirectories) {
		t.Fatalf("Sync() error = %v, want ErrNoScanDirectories", err)
	}
}

func TestNormalizeProviderDirsDropsEmptyDirectories(t *testing.T) {
	dirs := normalizeProviderDirs(map[Provider][]string{
		Provider("fixture"): {"fixture-dir"},
		ProviderCodex:       {""},
	})

	if got := dirs["fixture"]; len(got) != 1 || got[0] != "fixture-dir" {
		t.Fatalf("fixture dirs = %#v, want fixture-dir", got)
	}
	if got := dirs["codex"]; len(got) != 0 {
		t.Fatalf("codex dirs = %#v, want empty dirs dropped", got)
	}
}

func TestDefaultProviderDirsIncludesBuiltInProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs := DefaultProviderDirs()
	want := map[Provider]string{
		ProviderClaude:  filepath.Join(home, ".claude"),
		ProviderCodex:   filepath.Join(home, ".codex"),
		ProviderCopilot: filepath.Join(home, ".copilot"),
		ProviderGemini:  filepath.Join(home, ".gemini", "tmp"),
		ProviderKimi:    filepath.Join(home, ".kimi"),
		ProviderQwen:    filepath.Join(home, ".qwen"),
		ProviderPi:      filepath.Join(home, ".pi", "agent", "sessions"),
		ProviderAmp:     filepath.Join(home, ".local", "share", "amp"),
		ProviderDroid:   filepath.Join(home, ".factory", "sessions"),
		ProviderKilo:    filepath.Join(home, ".local", "share", "kilo"),
		ProviderHermes:  filepath.Join(home, ".hermes"),
		ProviderOpenCode: filepath.Join(home,
			".local", "share", "opencode",
		),
	}

	for provider, dir := range want {
		if got := dirs[provider]; len(got) == 0 || got[0] != dir {
			t.Fatalf("%s dirs = %#v, want first dir %q", provider, got, dir)
		}
	}
	if got := dirs[ProviderKimi]; len(got) != 2 || got[1] != filepath.Join(home, ".kimi-code") {
		t.Fatalf("kimi dirs = %#v, want .kimi and .kimi-code", got)
	}
	if got := dirs[ProviderOpenClaw]; len(got) != 4 {
		t.Fatalf("openclaw dirs = %#v, want four defaults", got)
	}
	if got := dirs[ProviderCodebuff]; len(got) != 3 {
		t.Fatalf("codebuff dirs = %#v, want three channel defaults", got)
	}
	if got := dirs[ProviderGoose]; len(got) != 3 {
		t.Fatalf("goose dirs = %#v, want three db path defaults", got)
	}
}

// Scanning is offline; a missing API key only means the upload half is
// skipped, so Sync succeeds and events queue locally for later.
func TestSyncWithoutAPIKeyScansOffline(t *testing.T) {
	client := newTestClient(t)
	claudeDir := t.TempDir()

	err := client.Sync(context.Background(), SyncOptions{
		ProviderDirs: map[Provider][]string{ProviderClaude: {claudeDir}},
	})
	if err != nil {
		t.Fatalf("Sync() without API key = %v, want offline scan to succeed", err)
	}
	if _, err := os.Stat(store.UsageDBPath(client.DataDir())); err != nil {
		t.Fatalf("usage database missing after offline sync: %v", err)
	}
}

// An editor may start sending heartbeats before the user signs in. The event
// must still be queued locally; only the upload is skipped.
func TestSendHeartbeatWithoutAPIKeyQueuesEvent(t *testing.T) {
	client := newTestClient(t)

	err := client.SendHeartbeat(context.Background(), Heartbeat{
		Entity: filepath.Join(t.TempDir(), "main.go"),
		Editor: "vscode",
	})
	if err != nil {
		t.Fatalf("SendHeartbeat() without API key = %v, want queued event", err)
	}

	usageDB, err := usagedb.Open(store.UsageDBPath(client.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()

	pending, err := usageDB.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending events = %d, want 1 queued heartbeat", len(pending))
	}
}

func TestSendHeartbeatCarriesTypedLines(t *testing.T) {
	client := newTestClient(t)

	err := client.SendHeartbeat(context.Background(), Heartbeat{
		Entity:       filepath.Join(t.TempDir(), "main.go"),
		Editor:       "vscode",
		Category:     "code reviewing",
		LinesAdded:   3,
		LinesRemoved: -1,
	})
	if err != nil {
		t.Fatalf("SendHeartbeat() = %v", err)
	}

	usageDB, err := usagedb.Open(store.UsageDBPath(client.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()

	pending, err := usageDB.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending events = %d, want 1", len(pending))
	}
	entry := pending[0]
	if entry.LinesAdded != 3 || entry.LinesRemoved != 0 {
		t.Fatalf("lines = +%d/-%d, want +3/-0 (a negative count is nothing typed)", entry.LinesAdded, entry.LinesRemoved)
	}
	if entry.Category != "code reviewing" {
		t.Fatalf("category = %q, want it kept verbatim", entry.Category)
	}
}

func TestSendHeartbeatIdentityFileOverridesEditor(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "local-checkout")
	mustMkdirAll(t, filepath.Join(projectDir, ".git"))
	mustWriteFile(t, filepath.Join(projectDir, ".tokitoki"), "stable-dashboard-name\nstable-branch\n")
	entity := filepath.Join(projectDir, "src", "main.go")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      entity,
		Editor:      "vscode",
		Project:     "editor-detected-name",
		ProjectPath: filepath.Dir(entity),
		Branch:      "editor-branch",
	})
	if entry.Project != "stable-dashboard-name" || entry.ProjectPath != projectDir || entry.Branch != "stable-branch" {
		t.Fatalf("identity = %q %q %q, want the identity file's", entry.Project, entry.ProjectPath, entry.Branch)
	}
}

// Editors that read the branch themselves (Sakura) send it, but the checkout
// is where the branch lives; what the editor says only fills in when the
// checkout cannot say.
func TestSendHeartbeatCheckoutBranchWinsOverEditor(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	mustWriteFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/dev\n")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(repo, "main.go"),
		Editor:      "sakura",
		ProjectPath: repo,
		Branch:      "editor-branch",
	})
	if entry.Branch != "dev" {
		t.Fatalf("branch = %q, want the checkout's dev", entry.Branch)
	}
}

func TestSendHeartbeatCarriesTheCheckoutsRemote(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "payments-api")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	mustWriteFile(t, filepath.Join(repo, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/payments-api.git\n")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(repo, "src", "main.go"),
		Editor:      "vscode",
		ProjectPath: repo,
	})
	if entry.GitRemote != "git@github.com:acme/payments-api.git" {
		t.Fatalf("remote = %q, want origin's", entry.GitRemote)
	}
}

// An editor opened on a monorepo package reports the package folder; the
// work is in the repository, the same project the AI agents there report.
func TestSendHeartbeatEditorFolderInsideRepositoryIsTheRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "cps-dev")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	folder := filepath.Join(repo, "apps", "web")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(folder, "src", "page.tsx"),
		Editor:      "vscode",
		ProjectPath: folder,
		Branch:      "main",
	})
	if entry.Project != "cps-dev" || entry.ProjectPath != repo || entry.Branch != "main" {
		t.Fatalf("identity = %q %q %q, want cps-dev at %q on main", entry.Project, entry.ProjectPath, entry.Branch, repo)
	}
}

// An editor opened on a folder of repositories reports that folder; the
// file edited says which repository the work is in.
func TestSendHeartbeatEntityRepositoryWinsOverEditorFolder(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "tracklm")
	repo := filepath.Join(workspace, "tokitoki-cli")
	mustMkdirAll(t, filepath.Join(repo, ".git"))

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(repo, "cmd", "main.go"),
		Editor:      "vscode",
		ProjectPath: workspace,
	})
	if entry.Project != "tokitoki-cli" || entry.ProjectPath != repo {
		t.Fatalf("identity = %q %q, want tokitoki-cli at %q", entry.Project, entry.ProjectPath, repo)
	}
}

func TestSendHeartbeatFolderOutsideRepositoryNamesItself(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "notes")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(folder, "todo.md"),
		Editor:      "jetbrains",
		ProjectPath: folder,
	})
	if entry.Project != "notes" || entry.ProjectPath != folder {
		t.Fatalf("identity = %q %q, want notes at %q", entry.Project, entry.ProjectPath, folder)
	}
}

// IntelliJ and Eclipse builds released before repositories counted send their
// IDE project name as Project and ask `today --project` for that same name.
// The shared CLI is replaced under them by other editors' updates, so their
// heartbeats must keep landing under that name.
func TestSendHeartbeatExplicitProjectKeepsOlderEditorsWorking(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "cps-dev")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	folder := filepath.Join(repo, "apps", "web")

	entry := sendAndQueue(t, Heartbeat{
		Entity:      filepath.Join(folder, "src", "page.tsx"),
		Editor:      "IntelliJ IDEA",
		Project:     "Web App",
		ProjectPath: folder,
	})
	if entry.Project != "Web App" || entry.ProjectPath != folder {
		t.Fatalf("identity = %q %q, want the editor's Web App at %q", entry.Project, entry.ProjectPath, folder)
	}
}

// sendAndQueue sends one heartbeat from a fresh client and returns the event
// it queued.
func sendAndQueue(t *testing.T, heartbeat Heartbeat) usage.Entry {
	t.Helper()
	client := newTestClient(t)
	if err := client.SendHeartbeat(context.Background(), heartbeat); err != nil {
		t.Fatalf("SendHeartbeat() = %v", err)
	}
	usageDB, err := usagedb.Open(store.UsageDBPath(client.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()
	pending, err := usageDB.PendingEvents(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending events = %d, want 1", len(pending))
	}
	return pending[0]
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestSetAndGetHostname(t *testing.T) {
	t.Setenv("TOKITOKI_HOSTNAME", "")
	client := newTestClient(t)

	if err := client.SetHostname(" studio "); err != nil {
		t.Fatal(err)
	}
	name, err := client.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if name != "studio" {
		t.Fatalf("Hostname() = %q, want stored override", name)
	}

	// Clearing the override hands the label back to the system.
	if err := client.SetHostname(""); err != nil {
		t.Fatal(err)
	}
	name, err = client.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if name == "studio" {
		t.Fatal("Hostname() still returns the cleared override")
	}
}
