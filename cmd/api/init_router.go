package main

import (
	"visoria-control/internal/adapters/handlers"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitRouter(r *gin.Engine, handler *handlers.VisoriaHandler) {
	// Configuración de CORS profesional
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "http://localhost:8880", "*"}, // Agregado el puerto de tu backend
		AllowMethods:     []string{"POST", "GET", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 👉 AGREGAR ESTA LÍNEA PARA HACER PÚBLICOS LOS PDFS:
	r.Static("/pdfs", "./uploads/pdfs")

	// 👇 NUEVO: Servir tu carpeta de frontend
	// Esto expone todo el contenido de la carpeta "frontend" en la ruta "/app"
	r.Static("/js", "./frontend/js")

	// 👇 NUEVO: Redirigir la raíz ("/") directamente a tu index.html
	r.StaticFile("/", "./frontend/index.html")

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
