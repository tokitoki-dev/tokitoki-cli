package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) requireToken(c *gin.Context) {
	token, err := s.agent.Token()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		c.Abort()
		return
	}

	if c.GetHeader("Authorization") != "Bearer "+token {
		errorJSON(c, http.StatusUnauthorized, errUnauthorized)
		c.Abort()
		return
	}

	c.Next()
}

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/health") {
			return
		}

		logger.Info("http request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
		)
	}
}
