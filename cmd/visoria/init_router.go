package main

import (
	httpadapter "visoria-control/internal/adapters/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type handlers struct {
	config    *httpadapter.ConfigHandler
	campaigns *httpadapter.CampaignHandler
	templates *httpadapter.TemplateHandler
}

func InitRouter(r *gin.Engine, h handlers, allowedOrigins []string) {
	// CORS restringido a orígenes explícitos (el frontend es same-origin, así que
	// en producción basta con BASE_URL). Nunca "*" junto con credenciales.
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"POST", "GET", "PATCH", "PUT", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// Frontend estático. Los PDF NO se sirven públicamente: contienen datos de menores y salen
	// directo a WhatsApp (como medio) o al correo (como adjunto).
	r.Static("/js", "./frontend/js")
	r.StaticFile("/", "./frontend/index.html")
	r.StaticFile("/visoria", "./frontend/index.html")

	v1 := r.Group("/api/v1")
	{
		// Configuración maestra (Google Sheets)
		v1.GET("/config", h.config.Get)
		v1.POST("/config/sync", h.config.Sync)

		// Campañas: importación, validación y corrección de filas
		v1.GET("/campaigns", h.campaigns.List)
		v1.POST("/campaigns", h.campaigns.Create)
		v1.GET("/campaigns/:id", h.campaigns.Get)
		v1.PATCH("/campaigns/:id/recipients/:rid", h.campaigns.EditRecipient)

		// Documentos y envío
		v1.POST("/campaigns/:id/documents", h.campaigns.Documents)
		v1.POST("/campaigns/:id/dispatch", h.campaigns.Dispatch)
		v1.POST("/campaigns/:id/retry", h.campaigns.Retry)
		v1.POST("/campaigns/:id/resume", h.campaigns.Resume)
		v1.GET("/campaigns/:id/deliveries", h.campaigns.Deliveries)

		// Plantillas Word
		v1.GET("/templates", h.templates.List)
		v1.PUT("/templates/:key", h.templates.Install)
	}
}
