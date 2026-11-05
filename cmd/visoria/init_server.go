package main

import (
	"context"
	"log/slog"
	"os"
	"visoria-control/internal/adapters/chatwoot"
	httpadapter "visoria-control/internal/adapters/http"
	"visoria-control/internal/adapters/pdfgen"
	"visoria-control/internal/adapters/sheets"
	"visoria-control/internal/adapters/whatsapp"
	"visoria-control/internal/app"
	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

func InitServer(r *gin.Engine, cfg *config.Config) {
	// 1. Instanciar Adaptadores (Repositorios y APIs Externas)
	// Aquí inyectamos el lector de Google Sheets CSV
	tournamentRepo := sheets.NewCSVTournamentRepository()
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Use(func(c *gin.Context) {
		c.Set("ctx", ctx)
		c.Next()
	})

	InitRouter(r, handler, cfg.CORSOrigins)

	slog.Info("servidor iniciado", "port", cfg.Port, "env", cfg.Env)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("el servidor se detuvo", "error", err)
		os.Exit(1)
	}
}
