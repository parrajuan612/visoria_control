package httpadapter

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

const maxTemplateUpload = 25 << 20

// TemplateHandler expone /api/v1/templates.
type TemplateHandler struct{ uc ports.TemplateUseCase }

func NewTemplateHandler(uc ports.TemplateUseCase) *TemplateHandler { return &TemplateHandler{uc: uc} }

// List: GET /api/v1/templates — plantillas instaladas.
func (h *TemplateHandler) List(c *gin.Context) {
	list, err := h.uc.List(c.Request.Context())
	if err != nil {
		log.FromContext(c.Request.Context()).Error("listando plantillas", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error interno. Inténtalo de nuevo."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"templates": list})
}

// Install: PUT /api/v1/templates/:key (multipart: file .docx). Se valida antes de reemplazar;
// la anterior queda en anteriores/. 400 con el mensaje del cliente si la plantilla no sirve.
func (h *TemplateHandler) Install(c *gin.Context) {
	key := c.Param("key")
	if domain.TemplateName(key) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plantilla desconocida."})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTemplateUpload+1<<20)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "El archivo supera el tamaño máximo de 25 MB."})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sube la plantilla de Word (.docx)."})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".docx") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sube la plantilla de Word (.docx)."})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxTemplateUpload+1))
	if err != nil || len(data) > maxTemplateUpload {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "El archivo supera el tamaño máximo de 25 MB."})
		return
	}
	backup, err := h.uc.Install(c.Request.Context(), key, data)
	var ie *ports.InputError
	switch {
	case errors.As(err, &ie):
		c.JSON(http.StatusBadRequest, gin.H{"error": ie.Message})
	case err != nil:
		log.FromContext(c.Request.Context()).Error("instalando plantilla", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error interno. Inténtalo de nuevo."})
	default:
		msg := "Plantilla «" + domain.TemplateName(key) + "» actualizada."
		if backup != "" {
			msg += " La anterior quedó guardada en plantillas/anteriores."
		}
		c.JSON(http.StatusOK, gin.H{"message": msg, "respaldo": backup})
	}
}
