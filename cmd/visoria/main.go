package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"visoria-control/internal/adapters/asynqueue"
	"visoria-control/internal/adapters/redisstore"
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

	// Redis es obligatorio: cola, estados y SSE dependen de él. Se verifica noeviction/AOF.
	rdb := redisstore.New(cfg.Redis)
	defer rdb.Close()
	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	warnings, err := redisstore.Verify(startCtx, rdb)
	cancelStart()
	if err != nil {
		slog.Error("redis no está listo", "error", err)
		os.Exit(1)
	}
	for _, w := range warnings {
		slog.Warn(w)
	}
	redisOpt := asynqueue.RedisOpt(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	queue := asynqueue.NewClient(redisOpt)
	defer queue.Close()

	if roles.Has(RoleWorker) {
		worker := asynqueue.NewWorker(redisOpt, asynqueue.DefaultQueues, logger)
		registerHandlers(worker, logger)
		if err := worker.Start(); err != nil {
			slog.Error("no se pudo iniciar el worker", "error", err)
			os.Exit(1)
		}
		defer worker.Shutdown() // cierre ordenado completo (SIGTERM con la API): tarea 1.6
		slog.Info("worker iniciado", "queues", len(asynqueue.DefaultQueues))
	}
	if roles.Has(RoleScheduler) {
		slog.Info("rol scheduler sin tareas periódicas todavía (se agregan con imap:poll, cfg:sync, recordatorios)")
	}

	if !roles.Has(RoleAPI) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		slog.Info("señal recibida, cerrando")
		return
	}

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(RequestLogger(logger), Recovery())
	InitServer(r, cfg, rdb, queue)
}
