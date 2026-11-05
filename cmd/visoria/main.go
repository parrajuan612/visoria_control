package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/log"

	"github.com/gin-gonic/gin"
)

func main() {
	rolesFlag := flag.String("roles", defaultRoles, "roles a ejecutar, separados por coma: api, worker, scheduler (o all)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: %s [--roles=api,worker,scheduler]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	log.Setup(slog.LevelInfo) // JSON desde el primer mensaje; se reajusta con LOG_LEVEL
	roles, err := ParseRoles(*rolesFlag)
	if err != nil {
		slog.Error("argumentos inválidos", "error", err)
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("no se pudo iniciar", "error", err)
		os.Exit(1)
	}
	logger := log.Setup(log.ParseLevel(cfg.LogLevel))
	slog.Info("roles activos", "roles", roles.String())

	// worker y scheduler se activan en la tarea 1.4 (Redis + asynq).
	for _, r := range []Role{RoleWorker, RoleScheduler} {
		if roles.Has(r) {
			slog.Info("rol aún sin implementación; se activará con asynq (Fase 1.4)", "role", string(r))
		}
	}

	if !roles.Has(RoleAPI) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		return
	}

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(RequestLogger(logger), Recovery())
	InitServer(r, cfg)
}
