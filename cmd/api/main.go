package main

import (
	"log/slog"
	"os"

	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/log"

	"github.com/gin-gonic/gin"
)

func main() {
	log.Setup(slog.LevelInfo) // JSON desde el primer mensaje; se reajusta con LOG_LEVEL
	cfg, err := config.Load()
	if err != nil {
		slog.Error("no se pudo iniciar", "error", err)
		os.Exit(1)
	}
	logger := log.Setup(log.ParseLevel(cfg.LogLevel))
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(RequestLogger(logger), Recovery())
	InitServer(r, cfg)
}
