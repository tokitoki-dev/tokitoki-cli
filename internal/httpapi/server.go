package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/claudeusage"
	"github.com/labx/tracklm-goagent/internal/codexusage"
	"github.com/labx/tracklm-goagent/internal/usage"
	"github.com/labx/tracklm-goagent/internal/usagedb"
	"github.com/labx/tracklm-goagent/internal/usagescan"
)

const defaultAddr = "127.0.0.1:39391"

type Server struct {
	httpServer *http.Server
	agent      *agent.Agent
	usageDB    *usagedb.DB
	scanner    *usagescan.Scanner
	quitCh     chan struct{}
	quitOnce   sync.Once
}

func NewServer(agent *agent.Agent, usageDB *usagedb.DB, logger *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger(logger))

	server := &Server{
		agent:   agent,
		usageDB: usageDB,
		scanner: usagescan.New(usageDB),
		quitCh:  make(chan struct{}),
	}

	router.GET("/health", server.health)

	authorized := router.Group("/")
	authorized.Use(server.requireToken)
	authorized.GET("/status", server.status)
	authorized.GET("/settings", server.settings)
	authorized.PUT("/settings", server.saveSettings)
	authorized.POST("/heartbeat", server.heartbeat)
	authorized.POST("/sync", server.sync)
	authorized.GET("/usage/daily", server.dailyUsage)
	authorized.POST("/usage/scan", server.scanUsage)
	authorized.GET("/claude/usage/daily", server.claudeDailyUsage)
	authorized.POST("/quit", server.quit)

	server.httpServer = &http.Server{
		Addr:              defaultAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return server
}

func (s *Server) Addr() string {
	return s.httpServer.Addr
}

func (s *Server) Quit() <-chan struct{} {
	return s.quitCh
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) status(c *gin.Context) {
	status, err := s.agent.Status()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (s *Server) settings(c *gin.Context) {
	settings, err := s.agent.Settings()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (s *Server) saveSettings(c *gin.Context) {
	var settings agent.Settings
	if err := c.ShouldBindJSON(&settings); err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := s.agent.SaveSettings(settings); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) heartbeat(c *gin.Context) {
	var heartbeat agent.Heartbeat
	if err := c.ShouldBindJSON(&heartbeat); err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := s.agent.RecordHeartbeat(heartbeat); err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

func (s *Server) sync(c *gin.Context) {
	status, err := s.agent.Sync(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

type sourceResult struct {
	Provider usage.Provider `json:"provider"`
	Paths    []string       `json:"paths,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func (s *Server) dailyUsage(c *gin.Context) {
	project := c.Query("project")
	provider := c.DefaultQuery("provider", "all")
	sources := make([]sourceResult, 0, 2)

	if provider == "all" || provider == string(usage.ProviderClaude) {
		paths, err := claudeusage.ClaudePaths()
		source := sourceResult{Provider: usage.ProviderClaude, Paths: paths}
		if err != nil {
			source.Error = err.Error()
		}
		sources = append(sources, source)
	}

	if provider == "all" || provider == string(usage.ProviderCodex) {
		paths, err := codexusage.CodexPaths()
		source := sourceResult{Provider: usage.ProviderCodex, Paths: paths}
		if err != nil {
			source.Error = err.Error()
		}
		sources = append(sources, source)
	}

	if len(sources) == 0 {
		errorJSON(c, http.StatusBadRequest, errors.New("provider must be claude, codex, or all"))
		return
	}

	summaries, err := s.usageDB.DailyProjectSummaries(provider, project)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sources": sources,
		"data":    summaries,
	})
}

func (s *Server) scanUsage(c *gin.Context) {
	result, err := s.scanner.ScanAll()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

func (s *Server) claudeDailyUsage(c *gin.Context) {
	project := c.Query("project")
	paths, err := claudeusage.ClaudePaths()
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	summaries, err := claudeusage.DailyProjectSummaries(project)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"paths": paths,
		"data":  summaries,
	})
}

func (s *Server) quit(c *gin.Context) {
	s.quitOnce.Do(func() {
		close(s.quitCh)
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func errorJSON(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{
		"ok":      false,
		"message": err.Error(),
	})
}
