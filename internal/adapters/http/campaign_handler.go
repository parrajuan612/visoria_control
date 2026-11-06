package httpadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/platform/signedurl"
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
	uc       ports.CampaignUseCase
	docs     ports.DocumentUseCase
	dispatch ports.DispatchUseCase
	links    *signedurl.Signer // nil = sin enlaces firmados (el ZIP queda solo para localhost)
	now      func() time.Time
}

// WithLinks activa los enlaces firmados (ZIP del panel y PDF de las notas de Chatwoot).
func (h *CampaignHandler) WithLinks(s *signedurl.Signer) *CampaignHandler {
	h.links = s
	return h
}

func NewCampaignHandler(uc ports.CampaignUseCase, docs ports.DocumentUseCase, dispatch ports.DispatchUseCase) *CampaignHandler {
	return &CampaignHandler{uc: uc, docs: docs, dispatch: dispatch, now: time.Now}
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
	case errors.Is(err, ports.ErrNoConfig), errors.Is(err, ports.ErrCampaignLocked), errors.Is(err, ports.ErrGenerationRunning),
		errors.Is(err, ports.ErrDispatchRunning), errors.Is(err, ports.ErrCampaignPaused), errors.Is(err, ports.ErrNoPDFs):
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

// Dispatch: POST /api/v1/campaigns/:id/dispatch  {"channels": ["whatsapp","email"]} (opcional).
// Envía las propuestas de las filas válidas con su PDF. 202 con el resumen de lo encolado.
func (h *CampaignHandler) Dispatch(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		Channels []string `json:"channels"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "El cuerpo debe ser JSON, por ejemplo {\"channels\": [\"whatsapp\", \"email\"]}."})
			return
		}
	}
	res, err := h.dispatch.Dispatch(c.Request.Context(), id, ports.DispatchInput{Channels: req.Channels, By: operatorName})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "Envío en curso.", "resultado": res})
}

// Retry: POST /api/v1/campaigns/:id/retry — reenvía lo que falló por motivos no permanentes.
func (h *CampaignHandler) Retry(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	res, err := h.dispatch.Retry(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resultado": res})
}

// Resume: POST /api/v1/campaigns/:id/resume — reanuda una campaña pausada.
func (h *CampaignHandler) Resume(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.dispatch.Resume(c.Request.Context(), id); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Campaña reanudada."})
}

// Deliveries: GET /api/v1/campaigns/:id/deliveries — estado real de cada envío y contadores.
func (h *CampaignHandler) Deliveries(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	snap, err := h.dispatch.Snapshot(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, snap)
}

// localOnly: la API aún no tiene login y el ZIP reúne datos de menores, así que solo se
// atiende desde la propia máquina. Se mira la dirección REAL de la conexión (no X-Forwarded-For,
// que cualquiera puede falsificar) y se rechaza lo que llegue por un proxy: detrás de nginx la
// conexión también parece local y el ZIP quedaría expuesto.
func localOnly(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PDFsZip: GET /api/v1/campaigns/:id/pdfs.zip — descarga todos los PDF generados (localhost o enlace firmado).
func (h *CampaignHandler) PDFsZip(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	// Se permite desde el propio equipo (sin proxy) o con un enlace firmado y vigente (el que entrega el panel).
	if !localOnly(c.Request) && !h.links.Valid("zip:"+id, c.Query("t"), h.now()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Enlace no válido o vencido. Vuelva a pulsar «Descargar ZIP» en el panel."})
		return
	}
	// Se arma en memoria: si algo falla a medias se responde un error limpio y no un ZIP corrupto.
	var buf bytes.Buffer
	if _, err := h.uc.ExportPDFs(c.Request.Context(), id, &buf); err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="propuestas_`+id+`.zip"`)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

// zipLinkTTL: el enlace del ZIP vale lo justo para iniciar la descarga.
const zipLinkTTL = 10 * time.Minute

// ZipLink: POST /api/v1/campaigns/:id/pdfs-link — entrega una URL firmada y de corta vida para el ZIP.
// Detrás de nginx la conexión nunca parece local; el panel pide este enlace y navega a él.
func (h *CampaignHandler) ZipLink(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if h.links == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Los enlaces de descarga no están configurados: defina LINK_SECRET (mínimo 16 caracteres) en el servidor."})
		return
	}
	if _, err := h.uc.Get(c.Request.Context(), id); err != nil {
		h.fail(c, err)
		return
	}
	tok := h.links.Token("zip:"+id, h.now().Add(zipLinkTTL))
	c.JSON(http.StatusOK, gin.H{"url": "/api/v1/campaigns/" + id + "/pdfs.zip?t=" + tok, "vence_en_minutos": int(zipLinkTTL.Minutes())})
}

// PDFResource es el recurso que firma el enlace de un PDF concreto (lo usan el handler y main.go).
func PDFResource(campaignID, recipientID string) string {
	return "pdf:" + campaignID + ":" + recipientID
}

// PDFFile: GET /api/v1/campaigns/:id/recipients/:rid/pdf?t=… — muestra el PDF de un destinatario.
// El enlace firmado es la única credencial: lo abre quien atiende el chat en Chatwoot sin iniciar sesión.
func (h *CampaignHandler) PDFFile(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	rid, ok := idParam(c, "rid")
	if !ok {
		return
	}
	if !h.links.Valid(PDFResource(id, rid), c.Query("t"), h.now()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Enlace no válido o vencido."})
		return
	}
	data, name, err := h.uc.RecipientPDF(c.Request.Context(), id, rid)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Data(http.StatusOK, "application/pdf", data)
}
