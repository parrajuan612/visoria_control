package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"visoria-control/internal/platform/log"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

type VisoriaHandler struct {
	service ports.VisoriaService
}

func NewVisoriaHandler(s ports.VisoriaService) *VisoriaHandler {
	return &VisoriaHandler{service: s}
}

func (h *VisoriaHandler) UploadPlayersExcel(c *gin.Context) {
	// Tope de 10 MB (SDD §4.2): un Excel de jugadores pesa unos KB.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No se encontró el archivo 'file' en la petición"})
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Por favor sube un archivo con formato .xlsx"})
		return
	}

	players, summary, err := h.service.ProcessPlayersExcel(c.Request.Context(), file)
	switch {
	case errors.Is(err, ports.ErrNoConfig):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	case errors.Is(err, ports.ErrInvalidSpreadsheet):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	case err != nil:
		log.FromContext(c.Request.Context()).Error("error procesando el Excel", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error interno procesando el archivo"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Archivo procesado exitosamente",
		"data":    players,
		"resumen": summary,
	})
}

func (h *VisoriaHandler) GeneratePDFs(c *gin.Context) {
	var req struct {
		Players []domain.Player `json:"players"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos de jugadores inválidos"})
		return
	}

	paths, err := h.service.GenerateDocuments(c.Request.Context(), req.Players)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error generando PDFs: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "PDFs generados con éxito",
		"generated_count": len(paths),
	})
}

func (h *VisoriaHandler) SendWhatsApp(c *gin.Context) {
	var req struct {
		Players []domain.Player `json:"players"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos de jugadores inválidos"})
		return
	}

	// Configurar cabeceras mágicas para Streaming (SSE)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	// Contexto desacoplado de la petición (el envío sigue aunque cierren la pestaña)
	// pero conserva el logger con request_id.
	bgCtx := log.WithContext(context.Background(), log.FromContext(c.Request.Context()))

	progressChan := make(chan string)

	// Ejecutamos el envío en segundo plano
	go func() {
		defer close(progressChan)
		err := h.service.DispatchWhatsAppMessages(bgCtx, req.Players, progressChan)
		if err != nil {
			progressChan <- fmt.Sprintf("❌ Error fatal: %v", err)
		}
		progressChan <- "FIN"
	}()

	// Escuchamos el canal y le inyectamos los textos al navegador en vivo
	c.Stream(func(w io.Writer) bool {
		if msg, ok := <-progressChan; ok {
			c.SSEvent("message", msg)
			return true
		}
		return false
	})
}
