package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
	"github.com/tokitoki-dev/tokitoki-cli/pkg/agentlib"
)

func TestParseRunFlagsDefaultsToProviderDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags, ok := parseRunFlags(nil)
	if !ok {
		t.Fatal("parseRunFlags() ok = false, want true")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != filepath.Join(home, ".claude") {
		t.Fatalf("claude dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != filepath.Join(home, ".codex") {
		t.Fatalf("codex dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderAmp]; len(got) != 1 || got[0] != filepath.Join(home, ".local", "share", "amp") {
		t.Fatalf("amp dirs = %#v, want default home dir", got)
	}
}

func TestParseRunFlagsRejectsEmptyProviderDir(t *testing.T) {
	if _, ok := parseRunFlags([]string{"--provider-dir", "claude="}); ok {
		t.Fatal("parseRunFlags() ok = true, want false")
	}
}

func TestParseRunFlagsUsesExplicitProviderDirs(t *testing.T) {
	flags, ok := parseRunFlags([]string{
		"--provider-dir", "claude=/tmp/claude",
		"--provider-dir", "codex=/tmp/codex",
	})
	if !ok {
		t.Fatal("parseRunFlags() ok = false, want true")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != "/tmp/claude" {
		t.Fatalf("claude dirs = %#v, want explicit dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != "/tmp/codex" {
		t.Fatalf("codex dirs = %#v, want explicit dir", got)
	}
}

func TestRunSetKeyWritesAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	path := filepath.Join(home, config.DataDirName, "api_key")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tokitoki_test_key\n" {
		t.Fatalf("api_key = %q, want saved key", string(data))
	}
}

func TestRunSetKeyRejectsMissingKey(t *testing.T) {
	if code := run([]string{"set", "key"}); code != 2 {
		t.Fatalf("run(set key) = %d, want 2", code)
	}
}

func TestRunGetKeyReturnsErrorWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if code := run([]string{"get", "key"}); code != 1 {
		t.Fatalf("run(get key) = %d, want 1", code)
	}
	path := filepath.Join(home, config.DataDirName, "api_key")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "" {
		t.Fatalf("api_key = %q, want empty file", string(data))
	}
}

func TestRunGetKeyRejectsExtraArgs(t *testing.T) {
	if code := run([]string{"get", "key", "extra"}); code != 2 {
		t.Fatalf("run(get key extra) = %d, want 2", code)
	}
}

func TestRunHeartbeatUploadsUnifiedIDEEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	var payload usageupload.Payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/usage-events/batch" {
			t.Errorf("path = %q, want usage batch endpoint", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		accepted := []string{}
		if len(payload.Events) == 1 {
			accepted = append(accepted, payload.Events[0].ID)
		}
		_ = json.NewEncoder(w).Encode(usageupload.Response{OK: true, Accepted: accepted})
	}))
	defer server.Close()
	t.Setenv(usageupload.BaseURLEnv, server.URL)

	code := run([]string{
		"heartbeat",
		"--entity", "/repo/src/App.java",
		"--time", "1784188800.25",
		"--project", "repo",
		"--project-folder", "/repo",
		"--editor", "eclipse",
		"--plugin", "eclipse/4.40 tokitoki-eclipse/0.1.0",
		"--write",
		"--lineno", "7",
	})
	if code != 0 {
		t.Fatalf("run(heartbeat) = %d, want 0", code)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(payload.Events))
	}
	event := payload.Events[0]
	if event.SourceType != "ide" || event.SourceProvider != "eclipse" || event.EventKind != "heartbeat" {
		t.Fatalf("source fields = %+v, want Eclipse IDE heartbeat", event)
	}
	if event.Entity != "/repo/src/App.java" || event.Language != "Java" {
		t.Fatalf("entity/language = %q/%q, want Java file", event.Entity, event.Language)
	}
	if event.IsWrite == nil || !*event.IsWrite {
		t.Fatalf("is_write = %v, want true", event.IsWrite)
	}
	if got := event.Raw["line_number"]; got != float64(7) && got != 7 {
		t.Fatalf("line_number = %#v, want 7", got)
	}
}

func TestRunHeartbeatAppliesProjectIdentityFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code := run([]string{"set", "key", "tokitoki_test_key"}); code != 0 {
		t.Fatalf("run(set key) = %d, want 0", code)
	}

	projectDir := filepath.Join(t.TempDir(), "payments-api")
	entity := filepath.Join(projectDir, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(entity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectDir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entity, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, ".tokitoki-project"),
		[]byte("my-company/{project}\nrelease/2026\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var payload usageupload.Payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		accepted := []string{}
		if len(payload.Events) == 1 {
			accepted = append(accepted, payload.Events[0].ID)
		}
		_ = json.NewEncoder(w).Encode(usageupload.Response{OK: true, Accepted: accepted})
	}))
	defer server.Close()
	t.Setenv(usageupload.BaseURLEnv, server.URL)

	code := run([]string{
		"heartbeat",
		"--entity", entity,
		"--project", "editor-project",
		"--project-folder", filepath.Dir(entity),
		"--branch", "editor-branch",
		"--editor", "sakura",
	})
	if code != 0 {
		t.Fatalf("run(heartbeat) = %d, want 0", code)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(payload.Events))
	}
	event := payload.Events[0]
	if event.Project != "my-company/payments-api" {
		t.Fatalf("project = %q, want my-company/payments-api", event.Project)
	}
	if event.Branch != "release/2026" {
		t.Fatalf("branch = %q, want release/2026", event.Branch)
	}
}

func TestRunRejectsDaemonCommand(t *testing.T) {
	if code := run([]string{"daemon"}); code != 2 {
		t.Fatalf("run(daemon) = %d, want 2", code)
	}
}

func TestParseWorkerFlagsDefaultsToProviderDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags, ok := parseWorkerFlags("tokitoki __service-run", []string{"--interval", "30s"})
	if !ok {
		t.Fatal("parseWorkerFlags() ok = false, want true")
	}
	if flags.interval != 30*time.Second {
		t.Fatalf("interval = %s, want 30s", flags.interval)
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != filepath.Join(home, ".claude") {
		t.Fatalf("claude dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != filepath.Join(home, ".codex") {
		t.Fatalf("codex dirs = %#v, want default home dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderAmp]; len(got) != 1 || got[0] != filepath.Join(home, ".local", "share", "amp") {
		t.Fatalf("amp dirs = %#v, want default home dir", got)
	}
}

func TestParseServiceFlagsSupportsSystemInstall(t *testing.T) {
	flags, userService, ok := parseServiceFlags([]string{
		"--system",
		"--provider-dir", "claude=/tmp/claude",
		"--provider-dir", "codex=/tmp/codex",
		"--interval", "1m",
	})
	if !ok {
		t.Fatal("parseServiceFlags() ok = false, want true")
	}
	if userService {
		t.Fatal("userService = true, want false with --system")
	}
	if got := flags.providerDirs[agentlib.ProviderClaude]; len(got) != 1 || got[0] != "/tmp/claude" {
		t.Fatalf("claude dirs = %#v, want explicit dir", got)
	}
	if got := flags.providerDirs[agentlib.ProviderCodex]; len(got) != 1 || got[0] != "/tmp/codex" {
		t.Fatalf("codex dirs = %#v, want explicit dir", got)
	}
	if flags.interval != time.Minute {
		t.Fatalf("interval = %s, want 1m", flags.interval)
	}
}

func TestRunServiceRejectsUnknownAction(t *testing.T) {
	if code := run([]string{"service", "bogus"}); code != 2 {
		t.Fatalf("run(service bogus) = %d, want 2", code)
	}
}
