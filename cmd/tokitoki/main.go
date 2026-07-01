// Command tokitoki scans local Claude Code/Codex usage files and uploads the
// resulting events to the local TokiToki server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	daemonservice "github.com/kardianos/service"
	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/cli"
	"github.com/labx/tokitoki-agent/internal/store"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
)

const (
	uploadTimeout       = 2 * time.Minute
	commandLockTimeout  = uploadTimeout + 10*time.Second
	defaultSyncInterval = 5 * time.Minute
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return 0
	}
	if len(args) > 0 && args[0] == "set" {
		return runSet(args[1:])
	}
	if len(args) > 0 && args[0] == "get" {
		return runGet(args[1:])
	}
	if len(args) > 0 && args[0] == "__service-run" {
		return runServiceWorker(args[1:])
	}
	if len(args) > 0 && args[0] == "service" {
		return runService(args[1:])
	}

	flags := flag.NewFlagSet("tokitoki", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	claudeDir := flags.String("claude-dir", "", "Claude data directory to scan (e.g. ~/.claude); omit to skip Claude")
	codexDir := flags.String("codex-dir", "", "Codex data directory to scan (e.g. ~/.codex); omit to skip Codex")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki does not use subcommands; run `tokitoki --help`")
		return 2
	}
	if *claudeDir == "" && *codexDir == "" {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --claude-dir and/or --codex-dir")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	syncCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	if err := runSync(syncCtx, *claudeDir, *codexDir, os.Stdout); err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func runSync(ctx context.Context, claudeDir, codexDir string, out io.Writer) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dataDir, err := store.InitializeDataDir()
	if err != nil {
		return err
	}
	lock, err := store.AcquireDataLock(dataDir, commandLockTimeout)
	if err != nil {
		return err
	}
	defer lock.Close()

	fileStore, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	usageDB, err := usagedb.Open(filepath.Join(dataDir, store.UsageDBFile))
	if err != nil {
		return err
	}
	defer usageDB.Close()

	app := &cli.App{
		Agent:     agent.New(fileStore, logger),
		UsageDB:   usageDB,
		Scanner:   usagescan.New(usageDB),
		ClaudeDir: claudeDir,
		CodexDir:  codexDir,
		Out:       out,
	}

	return app.Sync(ctx)
}

func runSet(args []string) int {
	if len(args) != 2 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki set key <API_KEY>")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dataDir, err := store.InitializeDataDir()
	if err != nil {
		return fail(logger, err)
	}
	lock, err := store.AcquireDataLock(dataDir, commandLockTimeout)
	if err != nil {
		return fail(logger, err)
	}
	defer lock.Close()

	fileStore, err := store.Open(dataDir)
	if err != nil {
		return fail(logger, err)
	}
	app := &cli.App{
		Agent: agent.New(fileStore, logger),
		Out:   os.Stdout,
	}
	if err := app.SetAPIKey(args[1]); err != nil {
		return fail(logger, err)
	}
	return 0
}

func runGet(args []string) int {
	if len(args) != 1 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki get key")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dataDir, err := store.InitializeDataDir()
	if err != nil {
		return fail(logger, err)
	}
	fileStore, err := store.Open(dataDir)
	if err != nil {
		return fail(logger, err)
	}
	app := &cli.App{
		Agent: agent.New(fileStore, logger),
		Out:   os.Stdout,
	}
	if err := app.GetAPIKey(); err != nil {
		return fail(logger, err)
	}
	return 0
}

type workerFlags struct {
	claudeDir string
	codexDir  string
	interval  time.Duration
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

func runWorkerLoop(ctx context.Context, flags workerFlags) int {
	logger := defaultLogger()
	ticker := time.NewTicker(flags.interval)
	defer ticker.Stop()

	for {
		syncCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
		if err := runSync(syncCtx, flags.claudeDir, flags.codexDir, os.Stdout); err != nil {
			logger.Error("tokitoki sync failed", "error", err)
		}
		cancel()

		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

type serviceProgram struct {
	flags  workerFlags
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *serviceProgram) Start(_ daemonservice.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		_ = runWorkerLoop(ctx, p.flags)
	}()
	return nil
}

func (p *serviceProgram) Stop(_ daemonservice.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
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
	service, err := newService(flags, userService)
	if err != nil {
		return fail(defaultLogger(), err)
	}

	switch action {
	case "install":
		err = service.Install()
	case "uninstall":
		err = service.Uninstall()
	case "start":
		err = service.Start()
	case "stop":
		err = service.Stop()
	case "restart":
		err = service.Restart()
	case "status":
		var status daemonservice.Status
		status, err = service.Status()
		if err == nil {
			fmt.Fprintln(os.Stdout, serviceStatusString(status))
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service action %q\n", action)
		return 2
	}
	if err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func parseWorkerFlags(name string, args []string) (workerFlags, bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	claudeDir := flags.String("claude-dir", defaultClaudeDir(), "Claude data directory to scan")
	codexDir := flags.String("codex-dir", defaultCodexDir(), "Codex data directory to scan")
	interval := flags.Duration("interval", defaultSyncInterval, "sync interval")
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
	return workerFlags{
		claudeDir: *claudeDir,
		codexDir:  *codexDir,
		interval:  *interval,
	}, true
}

func parseServiceFlags(args []string) (workerFlags, bool, bool) {
	flags := flag.NewFlagSet("tokitoki service", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	claudeDir := flags.String("claude-dir", defaultClaudeDir(), "Claude data directory to scan")
	codexDir := flags.String("codex-dir", defaultCodexDir(), "Codex data directory to scan")
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
	return workerFlags{
		claudeDir: *claudeDir,
		codexDir:  *codexDir,
		interval:  *interval,
	}, !*system, true
}

func newService(flags workerFlags, userService bool) (daemonservice.Service, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	config := &daemonservice.Config{
		Name:        "tokitoki",
		DisplayName: "TokiToki",
		Description: "Sync local AI usage to TokiToki.",
		Executable:  executable,
		Arguments: []string{
			"__service-run",
			"--claude-dir", flags.claudeDir,
			"--codex-dir", flags.codexDir,
			"--interval", flags.interval.String(),
		},
		Option: daemonservice.KeyValue{
			"UserService": userService,
			"Restart":     "always",
		},
	}
	return daemonservice.New(&serviceProgram{flags: flags}, config)
}

func serviceStatusString(status daemonservice.Status) string {
	switch status {
	case daemonservice.StatusRunning:
		return "running"
	case daemonservice.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func defaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func defaultCodexDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func usage() {
	fmt.Fprint(os.Stderr, `tokitoki — upload local AI usage to http://localhost:9093

Usage:
  tokitoki [--claude-dir DIR] [--codex-dir DIR]
  tokitoki set key <API_KEY>
  tokitoki get key
  tokitoki service <install|uninstall|start|stop|restart|status> [options]

Each invocation scans the directories you pass and uploads their usage events
to http://localhost:9093/api/usage-events/batch. A provider is scanned only
when its directory is given; there is no default location. The API key is read
from ~/.tokitoki/api_key. Use tokitoki set key <API_KEY> to create or update
that file. Service mode defaults to ~/.claude and ~/.codex.

Examples:
  tokitoki set key tt_live_xxx
  tokitoki get key
  tokitoki --claude-dir ~/.claude --codex-dir ~/.codex
  tokitoki --codex-dir ~/.codex
  tokitoki service install
  tokitoki service status
`)
}

func defaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func fail(logger *slog.Logger, err error) int {
	logger.Error("tokitoki failed", "error", err)
	return 1
}
