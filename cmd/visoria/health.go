package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Healthz responde 200 si Redis contesta; 503 si no. Sin datos sensibles.
func Healthz(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "redis": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "redis": "up"})
	}
}
