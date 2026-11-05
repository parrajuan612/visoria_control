package httpadapter

import (
	"errors"
	"net/http"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

// ConfigHandler expone la sincronización y la consulta de la configuración maestra.
type ConfigHandler struct{ uc ports.ConfigUseCase }

func NewConfigHandler(uc ports.ConfigUseCase) *ConfigHandler { return &ConfigHandler{uc: uc} }

// Sync: POST /api/v1/config/sync   (cuerpo opcional: {"url": "https://docs.google.com/..."}).
//
//	200 → configuración válida aplicada
//	422 → la hoja tiene errores: NO se aplicó (se conserva la vigente); trae los hallazgos
//	400 → URL no válida o no configurada
//	502 → no se pudo descargar la hoja
func (h *ConfigHandler) Sync(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	// El cuerpo es opcional: sin él se usa la URL guardada o la del entorno.
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el cuerpo debe ser JSON, por ejemplo {\"url\": \"https://docs.google.com/...\"}"})
			return
		}
	}
	res, err := h.uc.Sync(c.Request.Context(), req.URL)
	switch {
	case errors.Is(err, ports.ErrInvalidSourceURL), errors.Is(err, ports.ErrNoSourceURL):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ports.ErrSourceUnavailable):
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno sincronizando la configuración"})
	case !res.Applied:
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "la hoja tiene errores; se conserva la configuración anterior", "resultado": res})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "Configuración sincronizada", "resultado": res})
	}
}

type becaDTO struct {
	Clave  string   `json:"clave"`
	Nombre string   `json:"nombre"`
	Total  string   `json:"total"`
	Pagos  []string `json:"pagos"`
}

type configDTO struct {
	Sincronizada   bool                    `json:"sincronizada"`
	EnlaceHoja     string                  `json:"enlace_hoja,omitempty"`
	Version        string                  `json:"version,omitempty"`
	SincronizadaEn *time.Time              `json:"sincronizada_en,omitempty"`
	Becas          []becaDTO               `json:"becas"`
	Torneos        []domain.TorneoRango    `json:"torneos"`
	Categorias     []domain.CategoriaRango `json:"categorias"`
	Hallazgos      []domain.Issue          `json:"hallazgos"`
}

// Get: GET /api/v1/config — configuración vigente (solo lectura) para la pantalla.
func (h *ConfigHandler) Get(c *gin.Context) {
	st, err := h.uc.Status(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno leyendo la configuración"})
		return
	}
	out := configDTO{Sincronizada: st.Sincronizada, EnlaceHoja: st.URL,
		Becas: []becaDTO{}, Torneos: []domain.TorneoRango{}, Categorias: []domain.CategoriaRango{}, Hallazgos: []domain.Issue{}}
	if st.Snapshot != nil {
		s := st.Snapshot
		out.Version, out.SincronizadaEn = s.Hash, &s.SyncedAt
		for _, b := range s.Config.Becas {
			d := becaDTO{Clave: string(b.Clave), Nombre: b.Nombre, Total: domain.FormatEUR(b.Total), Pagos: []string{}}
			for _, p := range b.Pagos {
				d.Pagos = append(d.Pagos, domain.FormatEUR(p))
			}
			out.Becas = append(out.Becas, d)
		}
		if s.Config.Torneos != nil {
			out.Torneos = s.Config.Torneos
		}
		if s.Config.Categorias != nil {
			out.Categorias = s.Config.Categorias
		}
		if s.Issues != nil {
			out.Hallazgos = s.Issues
		}
	}
	c.JSON(http.StatusOK, out)
}
