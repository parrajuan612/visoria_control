package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
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

func main() { os.Exit(run()) }

// run devuelve el código de salida; así los defer (cierre de Redis, colas) siempre se ejecutan.
func run() int {
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
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("no se pudo iniciar", "error", err)
		return 1
	}
	logger := log.Setup(log.ParseLevel(cfg.LogLevel))
	slog.Info("roles activos", "roles", roles.String())

	// SIGTERM/SIGINT cancelan ctx; un segundo Ctrl+C fuerza la salida inmediata.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Redis es obligatorio: cola, estados y SSE dependen de él. Se verifica noeviction/AOF.
	rdb := redisstore.New(cfg.Redis)
	defer rdb.Close()
	startCtx, cancelStart := context.WithTimeout(ctx, 10*time.Second)
	warnings, err := redisstore.Verify(startCtx, rdb)
	cancelStart()
	if err != nil {
		slog.Error("redis no está listo", "error", err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}
	redisOpt := asynqueue.RedisOpt(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	queue := asynqueue.NewClient(redisOpt)
	defer queue.Close()

	var worker *asynqueue.Worker
	if roles.Has(RoleWorker) {
		worker = asynqueue.NewWorker(redisOpt, asynqueue.DefaultQueues, logger)
		registerHandlers(worker, logger)
		if err := worker.Start(); err != nil {
			slog.Error("no se pudo iniciar el worker", "error", err)
			return 1
		}
		slog.Info("worker iniciado", "queues", len(asynqueue.DefaultQueues))
	}
	if roles.Has(RoleScheduler) {
		slog.Info("rol scheduler sin tareas periódicas todavía (se agregan con imap:poll, cfg:sync, recordatorios)")
	}

	code := 0
	if roles.Has(RoleAPI) {
		if cfg.Env == "production" {
			gin.SetMode(gin.ReleaseMode)
		}
		r := gin.New()
		r.Use(RequestLogger(logger), Recovery())
		SetupRoutes(r, cfg, rdb, queue)

		ln, err := net.Listen("tcp", ":"+cfg.Port)
		if err != nil {
			slog.Error("no se pudo abrir el puerto", "port", cfg.Port, "error", err)
			code = 1
		} else {
			slog.Info("servidor iniciado", "port", cfg.Port, "env", cfg.Env)
			if err := serve(ctx, newHTTPServer(ln.Addr().String(), r), ln, shutdownGrace); err != nil {
				slog.Error("la API terminó con error", "error", err)
				code = 1
			}
		}
	} else {
		<-ctx.Done()
		slog.Info("señal recibida, cerrando")
	}

	// Orden de cierre: primero la API (ya cerrada arriba, no encola más), luego los
	// workers (esperan las tareas en curso; las no terminadas quedan en Redis), y al
	// final Redis (defer).
	stop() // restaura el comportamiento por defecto: un segundo Ctrl+C mata el proceso
	if worker != nil {
		slog.Info("cerrando worker: se esperan las tareas en curso")
		worker.Shutdown()
		slog.Info("worker cerrado")
	}
	return code
}
