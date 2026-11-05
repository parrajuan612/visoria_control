package httpadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

const (
	maxExcelBytes = 10 << 20 // SDD §4.2: 10 MB
	maxFormBytes  = maxExcelBytes + 1<<20
	maxPatchBytes = 16 << 10
	// Hasta que exista el login (Fase 7) todas las acciones se registran a nombre del operador.
	operatorName = "operador"
)

var reID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// CampaignHandler expone /api/v1/campaigns (SDD §4.2 y §4.3).
type CampaignHandler struct {
	uc     ports.CampaignUseCase
	legacy ports.VisoriaService
	docs   ports.DocumentUseCase
}

func NewCampaignHandler(uc ports.CampaignUseCase, legacy ports.VisoriaService, docs ports.DocumentUseCase) *CampaignHandler {
	return &CampaignHandler{uc: uc, legacy: legacy, docs: docs}
}

// fail traduce los errores de la aplicación a respuestas HTTP. Los errores internos no se
// exponen al cliente (pueden traer rutas o datos): se registran con el request_id.
func (h *CampaignHandler) fail(c *gin.Context, err error) {
	var ie *ports.InputError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &ie):
		c.JSON(http.StatusBadRequest, gin.H{"error": ie.Message})
	case errors.As(err, &tooBig):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "El archivo supera el tamaño máximo de 10 MB."})
	case errors.Is(err, ports.ErrNoConfig), errors.Is(err, ports.ErrCampaignLocked), errors.Is(err, ports.ErrGenerationRunning):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ports.ErrInvalidSpreadsheet):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, ports.ErrCampaignNotFound), errors.Is(err, ports.ErrRecipientNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		log.FromContext(c.Request.Context()).Error("error en campañas", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error interno. Inténtalo de nuevo."})
	}
}

func idParam(c *gin.Context, name string) (string, bool) {
	v := c.Param(name)
	if !reID.MatchString(v) {
		c.JSON(http.StatusNotFound, gin.H{"error": "No encontrado."})
		return "", false
	}
	return v, true
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "si", "sí", "yes":
		return true
	}
	return false
}

// Create: POST /api/v1/campaigns (multipart: file, torneo, fecha_pago_1..3, requerir_ambos).
//
//	201 → campaña creada en DRAFT con su borrador validado
//	400 → torneo/fechas inválidos o faltan fechas · 409 → sin configuración · 413 → archivo grande · 422 → Excel inválido
func (h *CampaignHandler) Create(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFormBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.fail(c, err)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sube el Excel de los becados (.xlsx)."})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sube el Excel de los becados (.xlsx)."})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxExcelBytes+1))
	if err != nil {
		h.fail(c, err)
		return
	}
	if len(data) > maxExcelBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "El archivo supera el tamaño máximo de 10 MB."})
		return
	}

	name := filepath.Base(header.Filename)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	detail, err := h.uc.Create(c.Request.Context(), ports.CreateCampaignInput{
		Data: data, Filename: name, Torneo: c.PostForm("torneo"),
		FechasPago:  []string{c.PostForm("fecha_pago_1"), c.PostForm("fecha_pago_2"), c.PostForm("fecha_pago_3")},
		RequireBoth: truthy(c.PostForm("requerir_ambos")), By: operatorName,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, detail)
}

// List: GET /api/v1/campaigns.
func (h *CampaignHandler) List(c *gin.Context) {
	list, err := h.uc.List(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	if list == nil {
		list = []domain.CampaignView{}
	}
	c.JSON(http.StatusOK, gin.H{"campaigns": list})
}

// Get: GET /api/v1/campaigns/:id.
func (h *CampaignHandler) Get(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// EditRecipient: PATCH /api/v1/campaigns/:id/recipients/:rid  {"correo": "x@y.com", ...}
// Devuelve la fila revalidada y las demás filas cuyo estado cambió.
func (h *CampaignHandler) EditRecipient(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	rid, ok := idParam(c, "rid")
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxPatchBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "La solicitud es demasiado grande."})
		return
	}
	var patch domain.RecipientPatch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() // un campo mal escrito no debe ignorarse en silencio
	if err := dec.Decode(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cuerpo inválido: use solo jugador, club, beca, anio, acudiente, movil o correo (texto)."})
		return
	}
	res, err := h.uc.EditRecipient(c.Request.Context(), id, rid, patch, operatorName)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Players: GET /api/v1/campaigns/:id/players — puente TEMPORAL: entrega los destinatarios en el
// formato del flujo de envío actual (que aún recibe los jugadores desde el navegador).
func (h *CampaignHandler) Players(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	detail, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.legacy.ToPlayers(c.Request.Context(), detail.Recipients), "resumen": detail.Campaign.Resumen})
}

// Documents: POST /api/v1/campaigns/:id/documents — encola la generación de las propuestas (PDF).
//
//	202 → encolada (el progreso se ve en cada fila: «documento», y en el stream de eventos)
//	200 → no hay nada por generar · 400 → falta una plantilla · 409 → ya se genera / campaña bloqueada
func (h *CampaignHandler) Documents(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	res, err := h.docs.Generate(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if res.Queued == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "Todas las propuestas ya están generadas.", "resultado": res})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "Generando las propuestas.", "resultado": res})
}
