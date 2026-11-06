package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"visoria-control/internal/adapters/asynqueue"
	"visoria-control/internal/adapters/campaignfs"
	"visoria-control/internal/adapters/chatwoot"
	"visoria-control/internal/adapters/docx"
	"visoria-control/internal/adapters/email"
	"visoria-control/internal/adapters/excel"
	httpadapter "visoria-control/internal/adapters/http"
	"visoria-control/internal/adapters/libreoffice"
	"visoria-control/internal/adapters/redisstore"
	"visoria-control/internal/adapters/sheets"
	"visoria-control/internal/adapters/templatefs"
	"visoria-control/internal/adapters/whatsapp"
	"visoria-control/internal/adapters/workspace"
	"visoria-control/internal/app"
	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/httpx"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/platform/ratelimit"
	"visoria-control/internal/platform/signedurl"
	"visoria-control/internal/ports"

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

	// Configuración maestra (Google Sheets): se restaura la última válida y, si no hay, se sincroniza.
	if cfg.SheetsURL != "" {
		if err := sheets.ValidateGoogleSheetsURL(cfg.SheetsURL); err != nil {
			slog.Error("SHEETS_CSV_URL no es válida", "error", err)
			return 1
		}
	}
	tournamentRepo := sheets.NewRepository()
	cfgSvc := app.NewConfigService(sheets.NewSource(), redisstore.NewConfigStore(rdb), tournamentRepo,
		cfg.SheetsURL, sheets.ValidateGoogleSheetsURL)
	files, err := campaignfs.New(cfg.DataDir)
	if err != nil {
		slog.Error("no se pudo preparar DATA_DIR", "dir", cfg.DataDir, "error", err)
		return 1
	}
	campaignRepo := redisstore.NewCampaignRepo(rdb)
	campaignSvc := app.NewCampaignService(excel.NewReader(), tournamentRepo, campaignRepo, files)

	// Documentos: plantillas Word en DATA_DIR/templates, relleno en Go y conversión con LibreOffice.
	templates, err := templatefs.New(filepath.Join(cfg.DataDir, "templates"))
	if err != nil {
		slog.Error("no se pudo preparar la carpeta de plantillas", "error", err)
		return 1
	}
	renderer := docx.NewRenderer()
	docSvc := app.NewDocumentService(app.DocumentDeps{
		Repo: campaignRepo, Files: files, Templates: templates, Render: renderer,
		Converter: libreoffice.New(libreoffice.Options{Binary: cfg.Docs.SofficePath, WorkDir: cfg.Docs.WorkDir, Timeout: cfg.Docs.Timeout}),
		Queue:     queue, Events: redisstore.NewEventPublisher(rdb), Locks: campaignSvc,
		Workspace: workspace.New(cfg.Docs.WorkDir), BatchSize: cfg.Docs.BatchSize,
	})
	tplSvc := app.NewTemplateService(templates, renderer)

	// Envío real: WhatsApp (Cloud API de Meta) y correo (Gmail SMTP). Un canal sin credenciales queda
	// deshabilitado y el despacho avisa con un mensaje claro.
	var waClient ports.WhatsAppClient
	if cfg.WhatsApp.Token != "" && cfg.WhatsApp.PhoneID != "" {
		waClient = whatsapp.NewClient(whatsapp.Options{Token: cfg.WhatsApp.Token, PhoneID: cfg.WhatsApp.PhoneID, APIVersion: cfg.WhatsApp.APIVersion})
	}
	var mailSender ports.EmailSender
	if cfg.SMTP.Enabled() {
		mailSender = email.NewSender(email.Options{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, User: cfg.SMTP.User, Password: cfg.SMTP.AppPassword,
			FromName: cfg.SMTP.FromName, Security: cfg.SMTP.Security})
	} else {
		slog.Warn("correo deshabilitado: defina SMTP_USER y SMTP_APP_PASSWORD para enviar por Gmail")
	}
	if len(cfg.DispatchAllowlist) > 0 {
		slog.Warn("DISPATCH_ALLOWLIST activa: SOLO se enviará a los móviles y correos de la lista", "entradas", len(cfg.DispatchAllowlist))
	}
	chatwootClient := chatwoot.New(cfg.Chatwoot.URL, cfg.Chatwoot.AccountID, cfg.Chatwoot.InboxID, cfg.Chatwoot.Token, httpx.New(httpx.ShortTimeout))
	pingCtx, cancelPing := context.WithTimeout(ctx, 8*time.Second)
	if err := chatwootClient.Ping(pingCtx); err != nil {
		// No impide arrancar (las notas no bloquean los envíos), pero sin esto no queda rastro en Chatwoot.
		slog.Warn("Chatwoot no responde: las notas de trazabilidad NO se registrarán; revise CHATWOOT_URL, CHATWOOT_API_TOKEN y CHATWOOT_ACCOUNT_ID",
			"url", cfg.Chatwoot.URL, "error", err)
	} else {
		slog.Info("Chatwoot accesible", "url", cfg.Chatwoot.URL, "inbox_id", cfg.Chatwoot.InboxID)
	}
	cancelPing()
	deliveries, err := redisstore.NewDeliveryRepo(rdb)
	if err != nil {
		slog.Error("no se pudo preparar el repositorio de entregas", "error", err)
		return 1
	}
	links := signedurl.New(cfg.LinkSecret)
	if links == nil {
		slog.Warn("LINK_SECRET ausente o demasiado corto (mínimo 32 caracteres; genere uno con `openssl rand -hex 32`, no use el texto de ejemplo): "+
			"las notas de Chatwoot no llevarán enlace al PDF y el ZIP solo se descargará desde localhost", "definido", cfg.LinkSecret != "")
	}
	pdfLink := func(campaignID, recipientID string) string {
		if links == nil || cfg.BaseURL == "" {
			return ""
		}
		exp := time.Now().Add(time.Duration(cfg.PDFLinkDays) * 24 * time.Hour)
		return cfg.BaseURL + "/api/v1/campaigns/" + campaignID + "/recipients/" + recipientID + "/pdf?t=" +
			links.Token(httpadapter.PDFResource(campaignID, recipientID), exp)
	}
	dispatchSvc := app.NewDispatchService(app.DispatchDeps{
		Repo: campaignRepo, Files: files, Deliveries: deliveries, Queue: queue, Events: redisstore.NewEventPublisher(rdb),
		WhatsApp: waClient, Email: mailSender, Composer: email.Composer{S: email.DefaultSettings(cfg.SMTP.WhatsAppContact)},
		Chatwoot: chatwootClient, PDFLink: pdfLink, Quota: redisstore.NewQuota(rdb),
		WALimit: ratelimit.PerSecond(cfg.WhatsApp.RatePerSec), EmailLimit: ratelimit.Every(cfg.SMTP.Pause), Locks: campaignSvc,
		Settings: app.DispatchSettings{WATemplate: cfg.WhatsApp.Template, WALanguage: cfg.WhatsApp.Language, EmailDaily: cfg.SMTP.DailyQuota, Allowlist: cfg.DispatchAllowlist},
	})
	if _, err := libreoffice.Find(cfg.Docs.SofficePath); err != nil {
		slog.Warn("LibreOffice no disponible: no se podrán generar las propuestas en PDF", "error", err)
	}
	restoreCtx, cancelRestore := context.WithTimeout(ctx, 30*time.Second)
	if err := cfgSvc.Restore(restoreCtx); err != nil {
		// No impide arrancar: se puede sincronizar luego desde la pantalla de configuración.
		slog.Warn("configuración maestra no disponible al arrancar", "error", err)
	}
	cancelRestore()

	var worker *asynqueue.Worker
	if roles.Has(RoleWorker) {
		worker = asynqueue.NewWorker(redisOpt, asynqueue.DefaultQueues, logger)
		registerHandlers(worker, logger, docSvc, dispatchSvc)
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
		SetupRoutes(r, cfg, rdb, services{config: cfgSvc, campaigns: campaignSvc, documents: docSvc, dispatch: dispatchSvc, templates: tplSvc})

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
