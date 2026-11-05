package main

import (
	"visoria-control/internal/adapters/handlers"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine, handler *handlers.VisoriaHandler, allowedOrigins []string) {
	// CORS restringido a orígenes explícitos (el frontend es same-origin, así que
	// en producción basta con BASE_URL). Nunca "*" junto con credenciales.
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"POST", "GET", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// Hacer públicos los PDFs y archivos JS
	r.Static("/pdfs", "./uploads/pdfs")
	r.Static("/js", "./frontend/js")

	// 👇 SOLUCIÓN: Servir el index.html tanto en la raíz ("/") como en ("/visoria")
	r.StaticFile("/", "./frontend/index.html")
	r.StaticFile("/visoria", "./frontend/index.html")

	v1 := r.Group("/api/v1")
	{
		// Configuración
		v1.POST("/config/load", handler.LoadConfig)

		// Flujo principal (Wizard)
		v1.POST("/players/upload", handler.UploadPlayersExcel)
		v1.POST("/documents/generate", handler.GeneratePDFs)
		v1.POST("/messages/send", handler.SendWhatsApp)
	}
}
