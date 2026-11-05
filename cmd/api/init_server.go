package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"visoria-control/internal/adapters/external"
	"visoria-control/internal/adapters/handlers"
	"visoria-control/internal/adapters/repository"
	"visoria-control/internal/core/services"

	"github.com/gin-gonic/gin"
)

func InitServer(r *gin.Engine) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8880"
	}

	// 1. Instanciar Adaptadores (Repositorios y APIs Externas)
	// Aquí inyectamos el lector de Google Sheets CSV
	tournamentRepo := repository.NewCSVTournamentRepository()
	pdfGen := external.NewPDFGenerator()
	waAPI := external.NewWhatsAppAPI()

	// 2. Instanciar el Servicio (Negocio) inyectando los puertos
	visoriaService := services.NewVisoriaService(tournamentRepo, pdfGen, waAPI)

	// 3. Instanciar Handlers inyectando el servicio
	handler := handlers.NewVisoriaHandler(visoriaService)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Use(func(c *gin.Context) {
		c.Set("ctx", ctx)
		c.Next()
	})

	// Orígenes permitidos: BASE_URL + CORS_ALLOWED_ORIGINS (separados por coma).
	// Se moverá a platform/config en la Fase 1.2.
	allowedOrigins := []string{"http://localhost:8880"}
	if base := strings.TrimRight(os.Getenv("BASE_URL"), "/"); base != "" {
		allowedOrigins = append(allowedOrigins, base)
	}
	for _, o := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowedOrigins = append(allowedOrigins, o)
		}
	}

	InitRouter(r, handler, allowedOrigins)

	fmt.Printf("🚀 Servidor corriendo en el puerto %s\n", port)
	r.Run(fmt.Sprintf(":%s", port))
}
