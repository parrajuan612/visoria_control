package main

import (
	httpadapter "visoria-control/internal/adapters/http"
	"visoria-control/internal/platform/config"
	"visoria-control/internal/platform/signedurl"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// services agrupa los casos de uso que necesita la API.
type services struct {
	config    ports.ConfigUseCase
	campaigns ports.CampaignUseCase
	documents ports.DocumentUseCase
	dispatch  ports.DispatchUseCase
	templates ports.TemplateUseCase
}

// SetupRoutes registra las rutas. No arranca el servidor.
func SetupRoutes(r *gin.Engine, cfg *config.Config, rdb *redis.Client, s services) {
	r.GET("/healthz", Healthz(rdb))
	InitRouter(r, handlers{
		config:    httpadapter.NewConfigHandler(s.config),
		campaigns: httpadapter.NewCampaignHandler(s.campaigns, s.documents, s.dispatch).WithLinks(signedurl.New(cfg.LinkSecret)),
		templates: httpadapter.NewTemplateHandler(s.templates),
	}, cfg.CORSOrigins)
}
