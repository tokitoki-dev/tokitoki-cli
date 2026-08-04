// Command tokitoki scans local Claude Code/Codex usage files and uploads the
// resulting events to the local Tokitoki server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/selfupdate"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usageupload"
	"github.com/tokitoki-dev/tokitoki-cli/pkg/agentlib"
)

const (
	defaultSyncInterval = 5 * time.Minute
	// defaultUploadInterval paces the queue drain. It runs far more often
	// than the scan because an empty queue costs one indexed query, and
	// because it bounds how long a freshly scanned event waits to be sent.
	defaultUploadInterval = 30 * time.Second
	// updateInterval paces the service worker's self-update checks. The
	// first check runs immediately after start, so a freshly installed or
	// relaunched service is current within one loop iteration.
	updateInterval = 12 * time.Hour
	updateTimeout  = 5 * time.Minute
)

// version comes from internal/buildinfo, the one place release builds stamp;
// go-install builds resolve it from the module version recorded in the
// binary. "dev" marks a local build, which never self-updates.
var version = buildinfo.Resolved()

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return 0
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(os.Stdout, version)
		return 0
	}
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(args[1:])
	}
	if len(args) > 0 && args[0] == "heartbeat" {
		return runHeartbeat(args[1:])
	}
	if len(args) > 0 && args[0] == "set" {
		return runSet(args[1:])
	}
	if len(args) > 0 && args[0] == "get" {
		return runGet(args[1:])
	}
	if len(args) > 0 && args[0] == "verify" {
		return runVerify(args[1:])
	}
	if len(args) > 0 && args[0] == "__service-run" {
		return runServiceWorker(args[1:])
	}
	if len(args) > 0 && args[0] == "service" {
		return runService(args[1:])
	}

	runFlags, ok := parseRunFlags(args)
	if !ok {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	syncCtx, cancel := context.WithTimeout(ctx, agentlib.DefaultUploadTimeout)
	syncErr := runSync(syncCtx, runFlags.providerDirs, os.Stdout)
	cancel()

	// The update check runs even after a failed sync: a broken deployment
	// must still be able to replace itself with a fixed release.
	if runFlags.checkUpdate {
		maybeCheckUpdate(defaultLogger())
	}
	if syncErr != nil {
		return fail(defaultLogger(), syncErr)
	}
	return 0
}

// maybeCheckUpdate self-updates at most once per updateInterval across
// runs. The stamp file's mtime is the whole record — written before the
// attempt so network failures are throttled the same as successes.
func maybeCheckUpdate(logger *slog.Logger) {
	dir, err := store.InitializeDataDir()
	if err != nil {
		logger.Warn("tokitoki update check skipped", "error", err)
		return
	}
	stamp := filepath.Join(dir, "last-update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < updateInterval {
		return
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		logger.Warn("tokitoki update check skipped", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	if _, err := selfupdate.Upgrade(ctx, logger, usageupload.BaseURL(), version); err != nil {
		logger.Warn("tokitoki self-update failed", "error", err)
	}
}

func runHeartbeat(args []string) int {
	flags := flag.NewFlagSet("tokitoki heartbeat", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	entity := flags.String("entity", "", "absolute path of the active file")
	timestamp := flags.Float64("time", 0, "heartbeat time as Unix seconds")
	project := flags.String("project", "", "project name")
	projectFolder := flags.String("project-folder", "", "absolute project root")
	language := flags.String("language", "", "file language")
	branch := flags.String("branch", "", "source-control branch")
	editor := flags.String("editor", "eclipse", "editor identifier")
	plugin := flags.String("plugin", "", "editor and plugin version")
	category := flags.String("category", "coding", "activity category")
	write := flags.Bool("write", false, "mark this heartbeat as a file write")
	lineNumber := flags.Int("lineno", 0, "one-based cursor line")
	cursorPosition := flags.Int("cursorpos", 0, "one-based cursor column")
	linesInFile := flags.Int("lines-in-file", 0, "number of lines in the file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki heartbeat does not accept positional arguments")
		return 2
	}
	if strings.TrimSpace(*entity) == "" {
		fmt.Fprintln(os.Stderr, "tokitoki heartbeat requires --entity")
		return 2
	}
	if strings.TrimSpace(*editor) == "" {
		fmt.Fprintln(os.Stderr, "tokitoki heartbeat requires --editor")
		return 2
	}

	heartbeatTime := time.Now().UTC()
	if *timestamp != 0 {
		seconds := int64(*timestamp)
		nanoseconds := int64((*timestamp - float64(seconds)) * float64(time.Second))
		heartbeatTime = time.Unix(seconds, nanoseconds).UTC()
	}

	client, err := agentlib.New(agentlib.Options{Logger: defaultLogger()})
	if err != nil {
		return fail(defaultLogger(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentlib.DefaultUploadTimeout)
	defer cancel()
	err = client.SendHeartbeat(ctx, agentlib.Heartbeat{
		Entity:         *entity,
		Timestamp:      heartbeatTime,
		Project:        *project,
		ProjectPath:    *projectFolder,
		Language:       *language,
		Branch:         *branch,
		Editor:         *editor,
		Plugin:         *plugin,
		Category:       *category,
		IsWrite:        *write,
		LineNumber:     *lineNumber,
		CursorPosition: *cursorPosition,
		LinesInFile:    *linesInFile,
	})
	if err != nil {
		return fail(defaultLogger(), err)
	}
	if err := writeJSON(os.Stdout, map[string]bool{"ok": true}); err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func parseRunFlags(args []string) (workerFlags, bool) {
	flags := flag.NewFlagSet("tokitoki", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	checkUpdate := flags.Bool("check-update", false, "self-update after the sync, at most once per 12h")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki does not use subcommands; run `tokitoki --help`")
		return workerFlags{}, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false
	}
	return workerFlags{
		providerDirs: dirs,
		explicitDirs: providerDirs.Explicit(),
		checkUpdate:  *checkUpdate,
	}, true
}

func runSync(ctx context.Context, providerDirs map[agentlib.Provider][]string, out io.Writer) error {
	client, err := agentlib.New(agentlib.Options{Logger: defaultLogger()})
	if err != nil {
		return err
	}
	if err := client.Sync(ctx, agentlib.SyncOptions{ProviderDirs: providerDirs}); err != nil {
		return err
	}
	return writeJSON(out, map[string]bool{"ok": true})
}

func runSet(args []string) int {
	if len(args) != 2 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki set key <API_KEY>")
		return 2
	}

	logger := defaultLogger()
	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		return fail(logger, err)
	}
	if err := client.SetAPIKey(args[1]); err != nil {
		return fail(logger, err)
	}
	if err := writeJSON(os.Stdout, map[string]bool{"ok": true}); err != nil {
		return fail(logger, err)
	}
	return 0
}

func runGet(args []string) int {
	if len(args) != 1 || (args[0] != "key" && args[0] != "dashboard-url") {
		fmt.Fprintln(os.Stderr, "usage: tokitoki get <key|dashboard-url>")
		return 2
	}

	logger := defaultLogger()
	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		return fail(logger, err)
	}

	var value string
	switch args[0] {
	case "key":
		value, err = client.GetAPIKey()
	case "dashboard-url":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		value, err = client.DashboardURL(ctx)
	}
	if err != nil {
		return fail(logger, err)
	}
	fmt.Fprintln(os.Stdout, value)
	return 0
}

func runVerify(args []string) int {
	if len(args) != 1 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki verify key")
		return 2
	}

	logger := defaultLogger()
	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		return fail(logger, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// An invalid key is a definite answer, not a failure: exit 0 with
	// valid:false so callers can tell it apart from "could not check".
	valid, err := client.VerifyAPIKey(ctx)
	if err != nil {
		return fail(logger, err)
	}
	if err := writeJSON(os.Stdout, map[string]any{"ok": true, "valid": valid}); err != nil {
		return fail(logger, err)
	}
	return 0
}

func runUpdate(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: tokitoki update")
		return 2
	}

	logger := defaultLogger()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	result, err := selfupdate.Upgrade(ctx, logger, usageupload.BaseURL(), version)
	if err != nil {
		return fail(logger, err)
	}
	if err := writeJSON(os.Stdout, map[string]any{
		"ok":      true,
		"updated": result.Updated,
		"version": result.Version,
	}); err != nil {
		return fail(logger, err)
	}
	return 0
}

type workerFlags struct {
	providerDirs map[agentlib.Provider][]string
	// explicitDirs records whether providerDirs came from --provider-dir
	// rather than the built-in defaults. Installed units only bake explicit
	// dirs into ExecStart: defaults must resolve from the service user's
	// home at run time, not the installer's at install time.
	explicitDirs   bool
	interval       time.Duration
	uploadInterval time.Duration
	checkUpdate    bool
}

func runServiceWorker(args []string) int {
	flags, ok := parseWorkerFlags("tokitoki __service-run", args)
	if !ok {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWorkerLoop(ctx, flags)
}

// runWorkerLoop scans and uploads on independent schedules.
//
// The two halves share nothing but the local queue: scanning writes events
// into it, uploading drains them. Running them on one ticker meant an upload
// could not start until a scan finished, so a cold start spent its whole scan
// with events queued and the network idle. Apart they proceed at their own
// pace, and the upload ticker runs faster because draining a queue that is
// usually empty costs one indexed query.
//
// Neither half knows the other exists. A scan that fails does not stop queued
// events from being sent, and an upload that fails does not stop new events
// from being queued.
func runWorkerLoop(ctx context.Context, flags workerFlags) int {
	logger := defaultLogger()

	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		logger.Error("tokitoki worker failed to start", "error", err)
		return 1
	}

	// A successful self-update replaces the binary on disk while this process
	// still runs the old code, so it stops every loop and exits for the
	// service manager to restart. Cancelling here is what ends the scan and
	// upload loops too.
	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		runIntervalLoop(workerCtx, flags.interval, func(context.Context) {
			if err := client.Scan(agentlib.SyncOptions{ProviderDirs: flags.providerDirs}); err != nil {
				logger.Error("tokitoki scan failed", "error", err)
			}
		})
	}()

	go func() {
		defer wg.Done()
		runIntervalLoop(workerCtx, flags.uploadInterval, func(runCtx context.Context) {
			if err := client.Upload(runCtx); err != nil {
				logger.Error("tokitoki upload failed", "error", err)
			}
		})
	}()

	runUpdateLoop(workerCtx, logger, flags.interval)
	stopWorkers()
	wg.Wait()
	return 0
}

// runIntervalLoop runs work immediately and then every interval until ctx is
// done. Each run is bounded by its own timeout so one slow pass cannot stall
// the schedule forever.
func runIntervalLoop(ctx context.Context, interval time.Duration, work func(context.Context)) {
	// NewTicker panics on a non-positive interval. A caller that never set one
	// wants the default cadence, not a crashed worker.
	if interval <= 0 {
		interval = defaultSyncInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		runCtx, cancel := context.WithTimeout(ctx, agentlib.DefaultUploadTimeout)
		work(runCtx)
		cancel()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runUpdateLoop checks for a new binary until ctx is done or one is
// installed. The caller then exits so the service manager restarts into it.
func runUpdateLoop(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	// Zero means "never checked", so the first iteration checks right away.
	var lastUpdateCheck time.Time
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if time.Since(lastUpdateCheck) >= updateInterval {
			lastUpdateCheck = time.Now()
			checkCtx, cancel := context.WithTimeout(ctx, updateTimeout)
			result, err := selfupdate.Upgrade(checkCtx, logger, usageupload.BaseURL(), version)
			cancel()
			if err != nil {
				logger.Warn("tokitoki self-update failed", "error", err)
			} else if result.Updated {
				// The binary on disk is new but this process is still the
				// old code. Stop; the service manager restarts us as the
				// new version.
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runService(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tokitoki service <install|uninstall|start|stop|restart|status> [options]")
		return 2
	}

	action := args[0]
	flags, userService, ok := parseServiceFlags(args[1:])
	if !ok {
		return 2
	}
	return platformService(action, flags, userService)
}

func parseWorkerFlags(name string, args []string) (workerFlags, bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	interval := flags.Duration("interval", defaultSyncInterval, "scan interval")
	uploadInterval := flags.Duration("upload-interval", defaultUploadInterval, "queue drain interval")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s does not accept positional arguments\n", name)
		return workerFlags{}, false
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be greater than zero")
		return workerFlags{}, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false
	}
	return workerFlags{
		providerDirs:   dirs,
		interval:       *interval,
		uploadInterval: *uploadInterval,
	}, true
}

func parseServiceFlags(args []string) (workerFlags, bool, bool) {
	flags := flag.NewFlagSet("tokitoki service", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	interval := flags.Duration("interval", defaultSyncInterval, "sync interval")
	system := flags.Bool("system", false, "install as a system service instead of a user service")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki service options must appear after the action")
		return workerFlags{}, false, false
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be greater than zero")
		return workerFlags{}, false, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false, false
	}
	return workerFlags{
		providerDirs:   dirs,
		explicitDirs:   providerDirs.Explicit(),
		interval:       *interval,
		uploadInterval: defaultUploadInterval,
	}, !*system, true
}

type providerDirFlags struct {
	dirs map[agentlib.Provider][]string
	set  bool
}

func newProviderDirFlags(defaults map[agentlib.Provider][]string) *providerDirFlags {
	return &providerDirFlags{dirs: copyProviderDirs(defaults)}
}

func (f *providerDirFlags) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(providerDirArgs(f.dirs), ",")
}

func (f *providerDirFlags) Set(value string) error {
	provider, dir, ok := strings.Cut(value, "=")
	provider = strings.TrimSpace(provider)
	dir = strings.TrimSpace(dir)
	if !ok || provider == "" || dir == "" {
		return fmt.Errorf("provider directory must use provider=dir")
	}
	if !f.set {
		f.dirs = make(map[agentlib.Provider][]string)
		f.set = true
	}
	f.dirs[agentlib.Provider(provider)] = append(f.dirs[agentlib.Provider(provider)], dir)
	return nil
}

func (f *providerDirFlags) ProviderDirs() map[agentlib.Provider][]string {
	if f == nil {
		return nil
	}
	return copyProviderDirs(f.dirs)
}

// Explicit reports whether any --provider-dir was passed, as opposed to the
// dirs being the built-in defaults.
func (f *providerDirFlags) Explicit() bool {
	return f != nil && f.set
}

func serviceArguments(flags workerFlags) []string {
	args := []string{"__service-run"}
	for _, value := range providerDirArgs(flags.providerDirs) {
		args = append(args, "--provider-dir", value)
	}
	return append(args, "--interval", flags.interval.String())
}

func providerDirArgs(providerDirs map[agentlib.Provider][]string) []string {
	values := make([]string, 0)
	providers := make([]agentlib.Provider, 0, len(providerDirs))
	for provider := range providerDirs {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i] < providers[j]
	})
	for _, provider := range providers {
		dirs := append([]string{}, providerDirs[provider]...)
		sort.Strings(dirs)
		for _, dir := range dirs {
			if dir != "" {
				values = append(values, fmt.Sprintf("%s=%s", provider, dir))
			}
		}
	}
	return values
}

func copyProviderDirs(providerDirs map[agentlib.Provider][]string) map[agentlib.Provider][]string {
	copied := make(map[agentlib.Provider][]string, len(providerDirs))
	for provider, dirs := range providerDirs {
		for _, dir := range dirs {
			if dir != "" {
				copied[provider] = append(copied[provider], dir)
			}
		}
	}
	return copied
}

func usage() {
	fmt.Fprint(os.Stderr, `tokitoki — upload local AI usage to the Tokitoki server

Usage:
  tokitoki [--provider-dir PROVIDER=DIR ...] [--check-update]
  tokitoki set key <API_KEY>
  tokitoki get key
  tokitoki get dashboard-url
  tokitoki verify key
  tokitoki heartbeat --entity FILE [options]
  tokitoki version
  tokitoki update
  tokitoki service <install|uninstall|start|stop|restart|status> [options]

Each invocation scans the provider roots you pass and uploads their usage
events to the Tokitoki server (TOKITOKI_BASE_URL, default
https://tokitoki.dev). By default, tokitoki scans the built-in roots for
claude, codex, copilot, gemini, kimi, qwen, openclaw, pi, amp, droid, kilo,
hermes, codebuff, opencode, goose, and workbuddy. Pass one or more
--provider-dir provider=dir values to scan an explicit provider set. The API
key is read from ~/.tokitoki/api_key; use tokitoki set key <API_KEY> to create
or update that file.

tokitoki update replaces this binary with the newest published release from
the same server. --check-update does the same after a sync, throttled to once
per 12 hours, and the resident service worker checks on the same cadence.
Local builds (version "dev") never self-update.

tokitoki service install keeps the sync running on its own. On Linux it
writes a systemd oneshot service plus timer: run it with sudo on servers to
get system units that need no login session, or as a plain user to get user
units (install then enables lingering so the timer survives logout). On macOS
and Windows it installs a resident worker via the OS service manager.

Examples:
  tokitoki set key tt_live_xxx
  tokitoki get key
  tokitoki get dashboard-url
  tokitoki heartbeat --entity /repo/main.go --project repo --project-folder /repo --editor eclipse
  tokitoki
  tokitoki --provider-dir gemini=~/.gemini/tmp --provider-dir amp=~/.local/share/amp
  tokitoki service install
  tokitoki service status
`)
}

func defaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// exitNoAPIKey marks the one failure a caller can act on: no key is
// configured. Front-ends prompt for a key on this code and treat every other
// non-zero exit as a transient problem to log and retry, instead of guessing
// from the error text.
const exitNoAPIKey = 3

func fail(logger *slog.Logger, err error) int {
	logger.Error("tokitoki failed", "error", err)
	if errors.Is(err, agentlib.ErrMissingAPIKey) {
		return exitNoAPIKey
	}
	return 1
}

func writeJSON(out io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
