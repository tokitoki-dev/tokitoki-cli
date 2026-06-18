// Command tokitoki is the cross-platform TokiToki agent.
//
// It is a stateless CLI: each invocation runs one subcommand and exits. Native
// front-ends (macOS / Windows / Linux) drive it by exec'ing it and parsing the
// JSON it writes to stdout. There is no daemon and no local HTTP server.
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

const uploadTimeout = 2 * time.Minute

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	if len(args) == 0 {
		usage()
		return 2
	}
	command, rest := args[0], args[1:]
	if command == "help" || command == "-h" || command == "--help" {
		usage()
		return 0
	}

	dataDir, err := store.InitializeDataDir()
	if err != nil {
		return fail(logger, "initialize data dir", err)
	}
	fileStore, err := store.Open(dataDir)
	if err != nil {
		return fail(logger, "open store", err)
	}

	app := &cli.App{
		Agent: agent.New(fileStore, logger),
		Out:   os.Stdout,
	}

	// Only the indexing/reading commands need the usage database, so only they
	// pay the cost of opening it (and taking its file lock).
	if needsUsageDB(command) {
		usageDB, err := usagedb.Open(filepath.Join(dataDir, store.UsageDBFile))
		if err != nil {
			return fail(logger, "open usage db", err)
		}
		defer usageDB.Close()
		app.UsageDB = usageDB
		app.Scanner = usagescan.New(usageDB)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, app, command, rest); err != nil {
		return fail(logger, command, err)
	}
	return 0
}

func needsUsageDB(command string) bool {
	switch command {
	case "scan", "upload", "sync", "daily", "status":
		return true
	default:
		return false
	}
}

func dispatch(ctx context.Context, app *cli.App, command string, args []string) error {
	switch command {
	case "scan":
		return app.Scan()

	case "upload":
		uploadCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
		defer cancel()
		return app.Upload(uploadCtx)

	case "sync":
		uploadCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
		defer cancel()
		return app.Sync(uploadCtx)

	case "daily":
		fs := flag.NewFlagSet("daily", flag.ContinueOnError)
		provider := fs.String("provider", "all", "all|claude|codex")
		project := fs.String("project", "", "filter by project name or path")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return app.Daily(*provider, *project)

	case "claude-daily":
		fs := flag.NewFlagSet("claude-daily", flag.ContinueOnError)
		project := fs.String("project", "", "filter by project name or path")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return app.ClaudeDaily(*project)

	case "config":
		return dispatchConfig(app, args)

	case "status":
		return app.Status()

	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func dispatchConfig(app *cli.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config requires a subcommand: get or set")
	}
	switch args[0] {
	case "get":
		return app.ConfigGet()
	case "set":
		fs := flag.NewFlagSet("config set", flag.ContinueOnError)
		apiKey := fs.String("api-key", "", "shared server API key")
		serverURL := fs.String("server-url", "", "server base URL")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		// Only update fields the caller actually passed.
		var apiKeyPtr, serverURLPtr *string
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "api-key":
				apiKeyPtr = apiKey
			case "server-url":
				serverURLPtr = serverURL
			}
		})
		return app.ConfigSet(apiKeyPtr, serverURLPtr)
	default:
		return fmt.Errorf("unknown config subcommand %q (want get or set)", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tokitoki — local AI coding usage agent

Usage:
  tokitoki <command> [flags]

Commands:
  scan                       Index changed Claude/Codex session files
  upload                     Upload indexed usage events to the server
  sync                       scan + upload (run this on a schedule)
  daily [--provider] [--project]
                             Summarize indexed usage by day/project (JSON)
  claude-daily [--project]   Summarize Claude usage directly from files
  config get                 Print settings
  config set [--api-key] [--server-url]
                             Update settings
  status                     Print indexed event count, sources, and config
  help                       Show this help

All commands write JSON to stdout and exit; there is no daemon.
`)
}

func fail(logger *slog.Logger, what string, err error) int {
	logger.Error(what, "error", err)
	return 1
}
