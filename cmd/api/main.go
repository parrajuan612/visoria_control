package main

import (
	"log/slog"
	"os"

	"visoria-control/internal/platform/config"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("no se pudo iniciar", "error", err)
		os.Exit(1)
	}
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	InitServer(r, cfg)
}
