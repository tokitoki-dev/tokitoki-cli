// Package agentlib exposes TokiToki's local usage sync engine for native
// front-ends.
package agentlib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agent"
	"github.com/tokitoki-dev/tokitoki-cli/internal/cli"
	"github.com/tokitoki-dev/tokitoki-cli/internal/config"
	"github.com/tokitoki-dev/tokitoki-cli/internal/deviceauth"
	"github.com/tokitoki-dev/tokitoki-cli/internal/langdetect"
	"github.com/tokitoki-dev/tokitoki-cli/internal/projectfile"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagedb"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usagescan"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
)

const (
	// DefaultUploadTimeout is the maximum duration for one scan and upload run.
	DefaultUploadTimeout = 2 * time.Minute

	// DefaultLockTimeout is the maximum duration to wait for another TokiToki
	// command to release the shared local data lock.
	DefaultLockTimeout = DefaultUploadTimeout + 10*time.Second
)

// Provider identifies a local AI usage source.
type Provider string

const (
	// ProviderClaude identifies Claude usage files.
	ProviderClaude Provider = "claude"

	// ProviderCodex identifies Codex usage files.
	ProviderCodex Provider = "codex"

	// ProviderCopilot identifies GitHub Copilot CLI usage files.
	ProviderCopilot Provider = "copilot"

	// ProviderGemini identifies Gemini CLI usage files.
	ProviderGemini Provider = "gemini"

	// ProviderKimi identifies Kimi usage files.
	ProviderKimi Provider = "kimi"

	// ProviderQwen identifies Qwen usage files.
	ProviderQwen Provider = "qwen"

	// ProviderOpenClaw identifies OpenClaw usage files.
	ProviderOpenClaw Provider = "openclaw"

	// ProviderPi identifies pi-agent usage files.
	ProviderPi Provider = "pi"

	// ProviderAmp identifies Amp usage files.
	ProviderAmp Provider = "amp"

	// ProviderDroid identifies Droid usage files.
	ProviderDroid Provider = "droid"

	// ProviderKilo identifies Kilo usage files.
	ProviderKilo Provider = "kilo"

	// ProviderHermes identifies Hermes Agent usage files.
	ProviderHermes Provider = "hermes"

	// ProviderCodebuff identifies Codebuff usage files.
	ProviderCodebuff Provider = "codebuff"

	// ProviderOpenCode identifies OpenCode usage files.
	ProviderOpenCode Provider = "opencode"

	// ProviderGoose identifies Goose usage files.
	ProviderGoose Provider = "goose"
)

var (
	// ErrMissingAPIKey is returned when the shared data directory does not have
	// a configured API key.
	ErrMissingAPIKey = errors.New("API key is not configured in ~/.tokitoki/api_key")

	// ErrNoScanDirectories is returned when a sync call has no provider
	// directory to scan.
	ErrNoScanDirectories = errors.New("nothing to scan; pass at least one provider directory")
)

// Options configures a Client.
type Options struct {
	// DataDir is the directory used for shared agent state. When empty, the
	// default is ~/.tokitoki.
	DataDir string

	// LockTimeout controls how long calls that mutate shared state wait for the
	// local data lock. When zero, DefaultLockTimeout is used.
	LockTimeout time.Duration

	// Logger receives warnings from lower-level agent components. When nil,
	// logs are discarded.
	Logger *slog.Logger
}

// SyncOptions selects provider data directories for one sync run.
type SyncOptions struct {
	// ProviderDirs selects data directories by provider. This is the extension
	// point for new local AI agents.
	ProviderDirs map[Provider][]string
}

// Heartbeat describes one WakaTime-style IDE activity sample.
type Heartbeat struct {
	Entity         string
	Timestamp      time.Time
	Project        string
	ProjectPath    string
	Language       string
	Branch         string
	Editor         string
	Plugin         string
	Category       string
	IsWrite        bool
	LineNumber     int
	CursorPosition int
	LinesInFile    int
}

// Client provides local settings and usage sync operations for native clients.
type Client struct {
	dataDir     string
	lockTimeout time.Duration
	logger      *slog.Logger
}

// New creates a Client and ensures its data directory exists.
func New(options Options) (*Client, error) {
	dataDir := options.DataDir
	var err error
	if dataDir == "" {
		dataDir, err = store.InitializeDataDir()
	} else {
		err = os.MkdirAll(dataDir, 0o700)
	}
	if err != nil {
		return nil, err
	}

	lockTimeout := options.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = DefaultLockTimeout
	}

	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Client{
		dataDir:     dataDir,
		lockTimeout: lockTimeout,
		logger:      logger,
	}, nil
}

// DataDir returns the directory used for shared agent state.
func (c *Client) DataDir() string {
	return c.dataDir
}

// SetAPIKey saves apiKey in the shared local agent store.
func (c *Client) SetAPIKey(apiKey string) error {
	return c.withDataLock(func() error {
		fileStore, err := store.Open(c.dataDir)
		if err != nil {
			return err
		}
		return agent.New(fileStore, c.logger).SaveAPIKey(apiKey)
	})
}

// GetAPIKey returns the configured API key.
func (c *Client) GetAPIKey() (string, error) {
	fileStore, err := store.Open(c.dataDir)
	if err != nil {
		return "", err
	}
	settings, err := agent.New(fileStore, c.logger).Settings()
	if err != nil {
		return "", err
	}
	if settings.APIKey == "" {
		return "", ErrMissingAPIKey
	}
	return settings.APIKey, nil
}

// BaseURL returns the TokiToki server every subsystem talks to — usage
// uploads, update checks, and the web dashboard alike. Front-ends open it
// when they need a plain link to the server (for example as the fallback
// when DashboardURL cannot mint a signed login link).
func BaseURL() string {
	return usageupload.BaseURL()
}

// DashboardURL exchanges the stored API key for a one-time browser login URL.
// Opening it signs the user straight into their web dashboard — no password.
func (c *Client) DashboardURL(ctx context.Context) (string, error) {
	apiKey, err := c.GetAPIKey()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return deviceauth.DashboardURL(ctx, usageupload.BaseURL(), apiKey)
}

// Sync scans selected provider directories and uploads newly discovered events.
func (c *Client) Sync(ctx context.Context, options SyncOptions) error {
	providerDirs := normalizeProviderDirs(options.ProviderDirs)
	if len(providerDirs) == 0 {
		return ErrNoScanDirectories
	}
	if ctx == nil {
		ctx = context.Background()
	}

	fileStore, err := store.Open(c.dataDir)
	if err != nil {
		return err
	}
	usageDB, err := usagedb.Open(filepath.Join(c.dataDir, store.UsageDBFile))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	scanner := usagescan.New(usageDB)
	scanner.Logger = c.logger
	app := &cli.App{
		Agent:        agent.New(fileStore, c.logger),
		UsageDB:      usageDB,
		Scanner:      scanner,
		ProviderDirs: providerDirs,
		Out:          io.Discard,
	}

	// Two phases, two locks. Ingestion mutates shared local state and runs
	// under the data lock; the drain talks to the network for up to the whole
	// upload timeout and must not make other processes' ingestion wait on it.
	if err := c.withDataLock(app.Ingest); err != nil {
		return err
	}
	return c.withUploadLock(func() error { return app.Upload(ctx) })
}

// SendHeartbeat persists an IDE activity event before attempting upload. If
// the network is unavailable the event stays queued in the shared local
// database and a later heartbeat or normal sync retries it with backoff.
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat Heartbeat) error {
	if strings.TrimSpace(heartbeat.Entity) == "" {
		return errors.New("heartbeat entity is required")
	}
	if strings.TrimSpace(heartbeat.Editor) == "" {
		return errors.New("heartbeat editor is required")
	}
	if heartbeat.Timestamp.IsZero() {
		heartbeat.Timestamp = time.Now().UTC()
	}
	// A project identity file is an optional override; one that exists but
	// cannot be read must not cost the heartbeat itself.
	if err := applyProjectFile(&heartbeat); err != nil {
		c.logger.Warn("project identity file ignored", "error", err)
	}
	if strings.TrimSpace(heartbeat.Project) == "" {
		heartbeat.Project = filepath.Base(strings.TrimSpace(heartbeat.ProjectPath))
	}
	if strings.TrimSpace(heartbeat.Project) == "" {
		heartbeat.Project = "unknown"
	}
	if strings.TrimSpace(heartbeat.Language) == "" {
		heartbeat.Language = langdetect.FromPath(heartbeat.Entity)
	}
	if strings.TrimSpace(heartbeat.Category) == "" {
		heartbeat.Category = "coding"
	}
	if ctx == nil {
		ctx = context.Background()
	}

	isWrite := heartbeat.IsWrite
	entry := usage.Entry{
		Provider:    usage.Provider(strings.ToLower(strings.TrimSpace(heartbeat.Editor))),
		SourceType:  "ide",
		EventKind:   "heartbeat",
		Timestamp:   heartbeat.Timestamp.UTC(),
		Date:        heartbeat.Timestamp.UTC().Format("2006-01-02"),
		Project:     strings.TrimSpace(heartbeat.Project),
		ProjectPath: strings.TrimSpace(heartbeat.ProjectPath),
		Language:    usage.NormalizeLanguage(heartbeat.Language),
		OS:          usage.NormalizeOS(runtime.GOOS),
		Client:      strings.TrimSpace(heartbeat.Editor),
		Entity:      strings.TrimSpace(heartbeat.Entity),
		EntityType:  "file",
		Branch:      strings.TrimSpace(heartbeat.Branch),
		Editor:      strings.TrimSpace(heartbeat.Editor),
		Category:    strings.TrimSpace(heartbeat.Category),
		IsWrite:     &isWrite,
		Raw: map[string]any{
			"plugin":          strings.TrimSpace(heartbeat.Plugin),
			"line_number":     heartbeat.LineNumber,
			"cursor_position": heartbeat.CursorPosition,
			"lines_in_file":   heartbeat.LinesInFile,
		},
	}
	entry.ID = usage.StableID(
		entry.SourceType,
		string(entry.Provider),
		entry.Entity,
		entry.Timestamp.Format(time.RFC3339Nano),
		fmt.Sprintf("%t", heartbeat.IsWrite),
	)

	usageDB, err := usagedb.Open(filepath.Join(c.dataDir, store.UsageDBFile))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	// Queue the event under the data lock, then drain under the upload lock.
	// The drain can take the whole network timeout; heartbeats from other
	// editors must be able to enqueue while it runs, not wait behind it.
	var settings agent.Settings
	if err := c.withDataLock(func() error {
		fileStore, err := store.Open(c.dataDir)
		if err != nil {
			return err
		}
		settings, err = agent.New(fileStore, c.logger).Settings()
		if err != nil {
			return err
		}
		if settings.APIKey == "" {
			return ErrMissingAPIKey
		}
		_, err = usageDB.InsertEvents([]usage.Entry{entry})
		return err
	}); err != nil {
		return err
	}

	return c.withUploadLock(func() error {
		return usageupload.SyncPending(ctx, settings, usageDB)
	})
}

func applyProjectFile(heartbeat *Heartbeat) error {
	resolved, found, err := projectfile.Resolve(projectfile.Input{
		Entity:      heartbeat.Entity,
		ProjectPath: heartbeat.ProjectPath,
		Branch:      heartbeat.Branch,
	})
	if err != nil {
		return fmt.Errorf("resolve project identity: %w", err)
	}
	if !found {
		return nil
	}
	heartbeat.Project = resolved.Project
	heartbeat.ProjectPath = resolved.ProjectPath
	heartbeat.Branch = resolved.Branch
	return nil
}

func normalizeProviderDirs(raw map[Provider][]string) map[usage.Provider][]string {
	providerDirs := make(map[usage.Provider][]string, len(raw))
	for provider, dirs := range raw {
		for _, dir := range dirs {
			if dir != "" {
				providerDirs[usage.Provider(provider)] = append(providerDirs[usage.Provider(provider)], dir)
			}
		}
	}
	return providerDirs
}

func (c *Client) withDataLock(fn func() error) error {
	lock, err := store.AcquireDataLock(c.dataDir, c.lockTimeout)
	if err != nil {
		return err
	}
	defer lock.Close()
	return fn()
}

// withUploadLock runs fn while holding the cross-process upload lock. When
// another process already holds it, that process is draining the same queue
// this one just wrote to, so there is nothing left to do here: the events are
// safely queued and "busy" is success, not failure.
func (c *Client) withUploadLock(fn func() error) error {
	lock, err := store.AcquireLock(c.dataDir, store.UploadLockFile, 0)
	if errors.Is(err, store.ErrLockBusy) {
		c.logger.Debug("another tokitoki process is uploading; events stay queued")
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	return fn()
}

// DefaultDataDir returns the shared TokiToki data directory.
func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, config.DataDirName), nil
}

// DefaultProviderDirs returns the built-in provider data directories.
func DefaultProviderDirs() map[Provider][]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return map[Provider][]string{
		ProviderClaude:   {filepath.Join(home, ".claude")},
		ProviderCodex:    {filepath.Join(home, ".codex")},
		ProviderCopilot:  {filepath.Join(home, ".copilot", "otel")},
		ProviderGemini:   {filepath.Join(home, ".gemini", "tmp")},
		ProviderKimi:     {filepath.Join(home, ".kimi")},
		ProviderQwen:     {filepath.Join(home, ".qwen")},
		ProviderOpenClaw: {filepath.Join(home, ".openclaw"), filepath.Join(home, ".clawdbot"), filepath.Join(home, ".moltbot"), filepath.Join(home, ".moldbot")},
		ProviderPi:       {filepath.Join(home, ".pi", "agent", "sessions")},
		ProviderAmp:      {filepath.Join(home, ".local", "share", "amp")},
		ProviderDroid:    {filepath.Join(home, ".factory", "sessions")},
		ProviderKilo:     {filepath.Join(home, ".local", "share", "kilo")},
		ProviderHermes:   {filepath.Join(home, ".hermes")},
		ProviderCodebuff: {filepath.Join(home, ".config", "manicode"), filepath.Join(home, ".config", "manicode-dev"), filepath.Join(home, ".config", "manicode-staging")},
		ProviderOpenCode: {filepath.Join(home, ".local", "share", "opencode")},
		ProviderGoose: {
			filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"),
			filepath.Join(home, "Library", "Application Support", "goose", "sessions", "sessions.db"),
			filepath.Join(home, ".local", "share", "Block", "goose", "sessions", "sessions.db"),
		},
	}
}
