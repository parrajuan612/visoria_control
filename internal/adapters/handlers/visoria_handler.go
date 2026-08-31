package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"visoria-control/internal/core/domain"
	"visoria-control/internal/core/ports"

	"github.com/gin-gonic/gin"
)

type VisoriaHandler struct {
	service ports.VisoriaService
}

func NewVisoriaHandler(s ports.VisoriaService) *VisoriaHandler {
	return &VisoriaHandler{service: s}
}

func (h *VisoriaHandler) LoadConfig(c *gin.Context) {
	var req struct {
		CsvURL string `json:"csv_url" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL del CSV es requerida"})
		return
	}

	if err := h.service.LoadMasterConfig(c.Request.Context(), req.CsvURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Configuración maestra sincronizada con éxito desde Google Sheets"})
}

func (h *VisoriaHandler) UploadPlayersExcel(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No se encontró el archivo 'file' en la petición"})
		return
	}
	defer file.Close()

	if !strings.HasSuffix(header.Filename, ".xlsx") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Por favor sube un archivo con formato .xlsx"})
		return
	}

	players, err := h.service.ProcessPlayersExcel(c.Request.Context(), file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error procesando el archivo: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Archivo procesado exitosamente",
		"data":    players,
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

	progressChan := make(chan string)

	// Ejecutamos el envío en segundo plano
	go func() {
		defer close(progressChan)
		err := h.service.DispatchWhatsAppMessages(context.Background(), req.Players, progressChan)
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
