package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/labx/tracklm-goagent/internal/agent"
	"github.com/labx/tracklm-goagent/internal/claudeusage"
)

const defaultAddr = "127.0.0.1:39391"

type Server struct {
	httpServer *http.Server
	agent      *agent.Agent
	quitCh     chan struct{}
	quitOnce   sync.Once
}

func NewServer(agent *agent.Agent, logger *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger(logger))

	server := &Server{
		agent:  agent,
		quitCh: make(chan struct{}),
	}

	router.GET("/health", server.health)

	authorized := router.Group("/")
	authorized.Use(server.requireToken)
	authorized.GET("/status", server.status)
	authorized.GET("/settings", server.settings)
	authorized.PUT("/settings", server.saveSettings)
	authorized.POST("/heartbeat", server.heartbeat)
	authorized.POST("/sync", server.sync)
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
