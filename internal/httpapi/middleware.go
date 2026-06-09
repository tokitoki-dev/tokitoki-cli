package httpapi

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

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
