package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"visoria-control/internal/platform/log"

	"github.com/gin-gonic/gin"
)

const requestIDHeader = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RequestLogger asigna un request_id (respeta el de nginx si es válido), lo
// deja en el contexto/logger y registra cada petición al terminar.
func RequestLogger(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		id := c.GetHeader(requestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		c.Header(requestIDHeader, id)

		lg := base.With("request_id", id)
		c.Request = c.Request.WithContext(log.WithContext(c.Request.Context(), lg))

		c.Next()

		lg.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", c.Writer.Size(),
		)
	}
}

// Recovery convierte un panic en 500 + log estructurado con el request_id.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.FromContext(c.Request.Context()).Error("panic en handler", "panic", rec)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
