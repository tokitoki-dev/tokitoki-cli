// Package agentlib exposes TokiToki's local usage sync engine for native
// front-ends.
package agentlib

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/cli"
	"github.com/labx/tokitoki-agent/internal/config"
	"github.com/labx/tokitoki-agent/internal/deviceauth"
	"github.com/labx/tokitoki-agent/internal/store"
	"github.com/labx/tokitoki-agent/internal/usageupload"
	"github.com/labx/tokitoki-agent/internal/usage"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
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

	return c.withDataLock(func() error {
		fileStore, err := store.Open(c.dataDir)
		if err != nil {
			return err
		}

		usageDB, err := usagedb.Open(filepath.Join(c.dataDir, store.UsageDBFile))
		if err != nil {
			return err
		}
		defer usageDB.Close()

		app := &cli.App{
			Agent:        agent.New(fileStore, c.logger),
			UsageDB:      usageDB,
			Scanner:      usagescan.New(usageDB),
			ProviderDirs: providerDirs,
			Out:          io.Discard,
		}
		return app.Sync(ctx)
	})
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
