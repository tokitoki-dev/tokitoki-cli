package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/httpapi"
	"github.com/labx/tracklm-goagent/internal/store"
	"github.com/labx/tracklm-goagent/internal/usagedb"
	"github.com/labx/tracklm-goagent/internal/usagescan"
	"github.com/labx/tracklm-goagent/internal/usageupload"
)

const (
	usageSyncInterval  = 15 * time.Minute
	usageUploadTimeout = 2 * time.Minute
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dataDir, err := store.DefaultDataDir()
	if err != nil {
		logger.Error("resolve data dir", "error", err)
		os.Exit(1)
	}

	fileStore, err := store.Open(dataDir)
	if err != nil {
		logger.Error("open store", "error", err)
		os.Exit(1)
	}

	usageDB, err := usagedb.Open(filepath.Join(dataDir, "usage.bolt"))
	if err != nil {
		logger.Error("open usage db", "error", err)
		os.Exit(1)
	}
	defer usageDB.Close()

	service := agent.New(fileStore, logger)
	server := httpapi.NewServer(service, usageDB, logger)

	usageSyncCtx, stopUsageSync := context.WithCancel(context.Background())
	defer stopUsageSync()
	usageSyncDone := make(chan struct{})
	go func() {
		defer close(usageSyncDone)
		runUsageSyncLoop(usageSyncCtx, logger, service, usageDB)
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("tracklm agent listening", "addr", server.Addr())
		errCh <- server.ListenAndServe()
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stopCh:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
		return
	case <-server.Quit():
		logger.Info("quit requested by api")
	}

	stopUsageSync()
	select {
	case <-usageSyncDone:
	case <-time.After(5 * time.Second):
		logger.Warn("usage sync loop did not stop before shutdown")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown server", "error", err)
		os.Exit(1)
	}
}

func runUsageSyncLoop(ctx context.Context, logger *slog.Logger, service *agent.Agent, usageDB *usagedb.DB) {
	scanAndUploadUsage(ctx, logger, service, usageDB)

	ticker := time.NewTicker(usageSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanAndUploadUsage(ctx, logger, service, usageDB)
		}
	}
}

func scanAndUploadUsage(ctx context.Context, logger *slog.Logger, service *agent.Agent, usageDB *usagedb.DB) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	result, err := usagescan.New(usageDB).ScanAll()
	if err != nil {
		logger.Warn("scan usage sessions", "error", err)
		return
	}
	logger.Info("usage session scan complete",
		"claude_files_seen", result.Claude.FilesSeen,
		"claude_events_inserted", result.Claude.EventsInserted,
		"codex_files_seen", result.Codex.FilesSeen,
		"codex_events_inserted", result.Codex.EventsInserted,
	)

	events, err := usageDB.UsageEvents()
	if err != nil {
		logger.Warn("load usage events for upload", "error", err)
		return
	}

	settings, err := service.Settings()
	if err != nil {
		logger.Warn("load settings for usage upload", "error", err)
		return
	}

	uploadCtx, cancel := context.WithTimeout(ctx, usageUploadTimeout)
	defer cancel()

	response, err := usageupload.Upload(uploadCtx, settings, events)
	if err != nil {
		logger.Warn("upload usage events",
			"error", err,
			"events", len(events),
			"server_url", uploadServerURL(settings.ServerURL),
		)
		return
	}

	logger.Info("usage events upload complete",
		"events", len(events),
		"accepted", len(response.Accepted),
		"duplicate", len(response.Duplicate),
		"server_url", uploadServerURL(settings.ServerURL),
	)
}

func uploadServerURL(serverURL string) string {
	if serverURL != "" {
		return serverURL
	}
	return usageupload.DefaultServerURL
}
