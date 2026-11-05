package main

import (
	"context"
	"log/slog"
	"os"
	"visoria-control/internal/adapters/external"
	"visoria-control/internal/adapters/handlers"
	"visoria-control/internal/adapters/repository"
	"visoria-control/internal/core/services"
	"visoria-control/internal/platform/config"

	"github.com/gin-gonic/gin"
)

func InitServer(r *gin.Engine, cfg *config.Config) {
	// 1. Instanciar Adaptadores (Repositorios y APIs Externas)
	// Aquí inyectamos el lector de Google Sheets CSV
	tournamentRepo := repository.NewCSVTournamentRepository()
	pdfGen := external.NewPDFGenerator()
	waAPI := external.NewWhatsAppAPI(cfg.WhatsApp.Token, cfg.WhatsApp.PhoneID, cfg.WhatsApp.APIVersion)

	// 2. Instanciar el Servicio (Negocio) inyectando los puertos
	visoriaService := services.NewVisoriaService(tournamentRepo, pdfGen, waAPI, services.Options{
		BaseURL:         cfg.BaseURL,
		ChatwootURL:     cfg.Chatwoot.URL,
		ChatwootToken:   cfg.Chatwoot.Token,
		ChatwootAccount: cfg.Chatwoot.AccountID,
		ChatwootInbox:   cfg.Chatwoot.InboxID,
		WhatsAppPause:   cfg.WhatsAppPause,
	})

	// 3. Instanciar Handlers inyectando el servicio
	handler := handlers.NewVisoriaHandler(visoriaService)

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
