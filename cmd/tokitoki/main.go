// Command tokitoki scans local Claude Code/Codex usage files and uploads the
// resulting events to the local TokiToki server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labx/tokitoki-agent/internal/agent"
	"github.com/labx/tokitoki-agent/internal/cli"
	"github.com/labx/tokitoki-agent/internal/store"
	"github.com/labx/tokitoki-agent/internal/usagedb"
	"github.com/labx/tokitoki-agent/internal/usagescan"
)

const (
	uploadTimeout      = 2 * time.Minute
	commandLockTimeout = uploadTimeout + 10*time.Second
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
	usageDB, err := usagedb.Open(filepath.Join(dataDir, store.UsageDBFile))
	if err != nil {
		return fail(logger, err)
	}
	defer usageDB.Close()

	app := &cli.App{
		Agent:     agent.New(fileStore, logger),
		UsageDB:   usageDB,
		Scanner:   usagescan.New(usageDB),
		ClaudeDir: *claudeDir,
		CodexDir:  *codexDir,
		Out:       os.Stdout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	syncCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	if err := app.Sync(syncCtx); err != nil {
		return fail(logger, err)
	}
	return 0
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

func usage() {
	fmt.Fprint(os.Stderr, `tokitoki — upload local AI usage to http://localhost:9093

Usage:
  tokitoki [--claude-dir DIR] [--codex-dir DIR]
  tokitoki set key <API_KEY>
  tokitoki get key

Each invocation scans the directories you pass and uploads their usage events
to http://localhost:9093/api/usage-events/batch. A provider is scanned only
when its directory is given; there is no default location. The API key is read
from ~/.tokitoki/api_key. Use tokitoki set key <API_KEY> to create or update
that file.

Examples:
  tokitoki set key tt_live_xxx
  tokitoki get key
  tokitoki --claude-dir ~/.claude --codex-dir ~/.codex
  tokitoki --codex-dir ~/.codex
`)
}

func fail(logger *slog.Logger, err error) int {
	logger.Error("tokitoki failed", "error", err)
	return 1
}
