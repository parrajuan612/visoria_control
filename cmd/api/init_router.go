package main

import (
	"visoria-control/internal/adapters/handlers"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine, handler *handlers.VisoriaHandler) {
	// Configuración de CORS profesional
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "*"}, // Ajusta en prod
		AllowMethods:     []string{"POST", "GET", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	v1 := r.Group("/api/v1")
	{
		// Configuración
		v1.POST("/config/load", handler.LoadConfig) // Carga el CSV de Google

		// Flujo principal (Wizard)
		v1.POST("/players/upload", handler.UploadPlayersExcel)
		v1.POST("/documents/generate", handler.GeneratePDFs)
		v1.POST("/messages/send", handler.SendWhatsApp)
	}
}
