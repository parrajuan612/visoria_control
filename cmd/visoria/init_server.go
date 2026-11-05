package main

import (
	"visoria-control/internal/adapters/chatwoot"
	httpadapter "visoria-control/internal/adapters/http"
	"visoria-control/internal/adapters/pdfgen"
	"visoria-control/internal/adapters/whatsapp"
	"visoria-control/internal/app"
	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/httpx"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// SetupRoutes crea los adaptadores, el servicio y registra las rutas. No arranca el servidor.
// queue se usará desde la Fase 3/5 (despacho de entregas).
func SetupRoutes(r *gin.Engine, cfg *config.Config, rdb *redis.Client, _ ports.TaskQueue,
	tournamentRepo ports.TournamentRepository, cfgSvc ports.ConfigUseCase) {
	// 1. Instanciar Adaptadores (Repositorios y APIs Externas)
	pdfs := pdfgen.New(pdfgen.PDFDir) // generador y a la vez DocumentStore
	waAPI := whatsapp.New(cfg.WhatsApp.Token, cfg.WhatsApp.PhoneID, cfg.WhatsApp.APIVersion)

	// 2. Instanciar el Servicio (Negocio) inyectando los puertos
	chatwootClient := chatwoot.New(cfg.Chatwoot.URL, cfg.Chatwoot.AccountID, cfg.Chatwoot.InboxID,
		cfg.Chatwoot.Token, httpx.New(httpx.ShortTimeout))

	visoriaService := app.NewVisoriaService(tournamentRepo, pdfs, pdfs, waAPI, chatwootClient, app.Options{
		BaseURL:       cfg.BaseURL,
		WhatsAppPause: cfg.WhatsAppPause,
	})

	// 3. Instanciar Handlers inyectando el servicio
	handler := httpadapter.NewVisoriaHandler(visoriaService)

	r.GET("/healthz", Healthz(rdb))
	InitRouter(r, handler, httpadapter.NewConfigHandler(cfgSvc), cfg.CORSOrigins)
}
