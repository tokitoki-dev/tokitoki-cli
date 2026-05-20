package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/httpapi"
	"github.com/labx/tracklm-goagent/internal/store"
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

	service := agent.New(fileStore, logger)
	server := httpapi.NewServer(service, logger)

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown server", "error", err)
		os.Exit(1)
	}
}
