// Package agentlib exposes Tokitoki's local usage sync engine for native
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
	"sync"
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

	// DefaultLockTimeout is the maximum duration to wait for another Tokitoki
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

	// ProviderWorkbuddy identifies WorkBuddy usage files.
	ProviderWorkbuddy Provider = "workbuddy"
)

var (
	// ErrMissingAPIKey is returned when the shared data directory does not have
	// a configured API key. It aliases the inner package's sentinel so a key
	// error raised anywhere in the stack compares equal here.
	ErrMissingAPIKey = cli.ErrNoAPIKey

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

// Heartbeat describes one IDE activity sample.
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

// BaseURL returns the Tokitoki server every subsystem talks to — usage
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

// VerifyAPIKey checks the stored API key against the server. A definite
// answer returns (true, nil) or (false, nil); network or server trouble is
// an error so callers can tell "invalid" apart from "could not check".
func (c *Client) VerifyAPIKey(ctx context.Context) (bool, error) {
	apiKey, err := c.GetAPIKey()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return deviceauth.VerifyKey(ctx, usageupload.BaseURL(), apiKey)
}

const (
	// syncDrainPoll is how often the upload half re-checks the queue while a
	// scan is still running.
	syncDrainPoll = 250 * time.Millisecond

	// syncDrainMinBatch is how many events must be queued before a drain runs
	// alongside a still-running scan.
	//
	// Without it the drain chases the scan: it sends whatever landed in the
	// last few milliseconds, so a scan producing a trickle of events turns
	// into a request per handful. Waiting for a worthwhile batch trades a
	// little latency — bounded by the scan itself, since the final drain sends
	// everything regardless — for far fewer, fuller requests.
	syncDrainMinBatch = 500
)

// Sync scans selected provider directories and uploads newly discovered
// events. Scanning is local and always runs; without a configured API key the
// events simply stay queued and upload resumes once a key is saved.
//
// The two halves run at the same time. A scan queues each file's events as it
// finishes that file, so the upload half has work to send long before the scan
// is done — on a first run over a large history that is the difference between
// uploading throughout the scan and sitting idle until it ends. They share no
// state but the queue: the scan writes to it, the drain reads from it.
//
// Sync returns only once both halves are finished, because callers are
// one-shot processes that exit when it returns. The drain therefore keeps
// polling until the scan has stopped producing and the queue is empty.
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
	settings, err := agent.New(fileStore, c.logger).Settings()
	if err != nil {
		return err
	}

	usageDB, err := usagedb.Open(store.UsageDBPath(c.dataDir))
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

	// Without a key there is nothing to drain into, so the scan runs alone and
	// its events wait in the queue for a run that has one.
	if settings.APIKey == "" {
		c.logger.Debug("skip upload; API key is not configured")
		return c.withDataLock(app.Ingest)
	}

	scanDone := make(chan struct{})
	var scanErr, uploadErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer close(scanDone)
		// Ingestion mutates shared local state and runs under the data lock;
		// the drain talks to the network and holds no lock, so a slow upload
		// never makes another process's ingestion wait.
		scanErr = c.withDataLock(app.Ingest)
	}()

	go func() {
		defer wg.Done()
		uploadErr = c.drainUntil(ctx, settings, usageDB, scanDone)
	}()

	wg.Wait()
	return errors.Join(scanErr, uploadErr)
}

// drainUntil uploads queued events until done is closed and the queue has been
// drained after that, or until ctx ends.
//
// The final drain after done matters: the scan's last file is queued moments
// before it finishes, and a drain that stopped at the same instant would leave
// those events for the next run.
func (c *Client) drainUntil(ctx context.Context, settings agent.Settings, usageDB *usagedb.DB, done <-chan struct{}) error {
	for {
		select {
		case <-done:
			// The scan has stopped adding work, so there is nothing left to
			// wait for. This pass sends everything, however little, and its
			// error is the one worth reporting: it is the last chance these
			// events had to go out during this run.
			return usageupload.SyncPending(ctx, settings, usageDB)
		case <-ctx.Done():
			// The run is out of time. The scan's events are queued and a later
			// run sends them, so this is not a failure of the sync.
			return nil
		default:
		}

		// While the scan is still producing, send only once enough has piled
		// up to fill a request. Draining on every tick would chase the scan
		// and spend a round-trip on whatever handful arrived since the last
		// one.
		queued, err := usageDB.PendingCount(time.Now())
		if err != nil {
			return err
		}
		if queued >= syncDrainMinBatch {
			// One batch per pass. Draining to empty here would follow the scan
			// down to its trickle; stopping lets the queue refill.
			//
			// A failure here is not fatal to the run. The scan is still going,
			// and returning would leave it with no uploader at all — including
			// for the final drain, which is where the events actually need to
			// be sent. Uploads retry with backoff, so the next pass tries
			// again and the final drain reports whatever still fails.
			if err := usageupload.SyncPendingBatches(ctx, settings, usageDB, 1); err != nil {
				c.logger.Debug("mid-scan upload failed; will retry", "error", err)
			}
		}

		select {
		case <-done:
			return usageupload.SyncPending(ctx, settings, usageDB)
		case <-ctx.Done():
			return nil
		case <-time.After(syncDrainPoll):
		}
	}
}

// Upload drains queued events to the server without scanning first.
//
// Scanning and uploading share no state but the local queue: a scan writes
// events into it and an upload drains them. Nothing about the drain depends on
// a scan having just run, so a caller that wants the two to proceed at their
// own pace runs Scan and Upload on separate schedules — an upload no longer
// waits for a scan to finish before sending what is already queued, and a slow
// or failing scan cannot hold back events that were queued minutes ago.
//
// A missing API key is not an error here. Events stay queued until a key
// exists, which is the same thing Sync does.
func (c *Client) Upload(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fileStore, err := store.Open(c.dataDir)
	if err != nil {
		return err
	}
	settings, err := agent.New(fileStore, c.logger).Settings()
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		c.logger.Debug("skip upload; API key is not configured")
		return nil
	}

	usageDB, err := usagedb.Open(store.UsageDBPath(c.dataDir))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	return usageupload.SyncPending(ctx, settings, usageDB)
}

// Scan ingests provider directories into the local queue without uploading.
// It is the other half of Sync, for callers running the two on separate
// schedules.
func (c *Client) Scan(options SyncOptions) error {
	providerDirs := normalizeProviderDirs(options.ProviderDirs)
	if len(providerDirs) == 0 {
		return ErrNoScanDirectories
	}

	usageDB, err := usagedb.Open(store.UsageDBPath(c.dataDir))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	scanner := usagescan.New(usageDB)
	scanner.Logger = c.logger
	app := &cli.App{
		UsageDB:      usageDB,
		Scanner:      scanner,
		ProviderDirs: providerDirs,
		Out:          io.Discard,
	}
	return c.withDataLock(app.Ingest)
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

	usageDB, err := usagedb.Open(store.UsageDBPath(c.dataDir))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	// Queue the event under the data lock, then drain without one.
	// The drain can take the whole network timeout; heartbeats from other
	// editors must be able to enqueue while it runs, not wait behind it.
	//
	// Queueing is local work and never depends on the API key: an editor that
	// starts sending heartbeats before the user configures one must not drop
	// them. The key only gates the upload half below.
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
		_, err = usageDB.InsertEvents([]usage.Entry{entry})
		return err
	}); err != nil {
		return err
	}

	if settings.APIKey == "" {
		c.logger.Debug("skip upload; API key is not configured")
		return nil
	}
	return usageupload.SyncPending(ctx, settings, usageDB)
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

// DefaultDataDir returns the shared Tokitoki data directory.
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
		ProviderCopilot:  {filepath.Join(home, ".copilot")},
		ProviderGemini:   {filepath.Join(home, ".gemini", "tmp")},
		ProviderKimi:     {filepath.Join(home, ".kimi"), filepath.Join(home, ".kimi-code")},
		ProviderQwen:     {filepath.Join(home, ".qwen")},
		ProviderOpenClaw: {filepath.Join(home, ".openclaw"), filepath.Join(home, ".clawdbot"), filepath.Join(home, ".moltbot"), filepath.Join(home, ".moldbot")},
		ProviderPi:       {filepath.Join(home, ".pi", "agent", "sessions")},
		ProviderAmp:      {filepath.Join(home, ".local", "share", "amp")},
		ProviderDroid:    {filepath.Join(home, ".factory", "sessions")},
		ProviderKilo:     {filepath.Join(home, ".local", "share", "kilo")},
		ProviderHermes:   {filepath.Join(home, ".hermes")},
		ProviderCodebuff: {filepath.Join(home, ".config", "manicode"), filepath.Join(home, ".config", "manicode-dev"), filepath.Join(home, ".config", "manicode-staging")},
		ProviderOpenCode: {
			filepath.Join(home, ".local", "share", "opencode"),
			filepath.Join(home, "Library", "Application Support", "opencode"),
			filepath.Join(home, "AppData", "Local", "opencode"),
			filepath.Join(home, "AppData", "Roaming", "opencode"),
		},
		ProviderGoose: {
			filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"),
			filepath.Join(home, "Library", "Application Support", "goose", "sessions", "sessions.db"),
			filepath.Join(home, ".local", "share", "Block", "goose", "sessions", "sessions.db"),
		},
		ProviderWorkbuddy: {filepath.Join(home, ".workbuddy")},
	}
}
