package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// DocumentService genera las propuestas en PDF de una campaña (SDD §4.4): una cola `docs` con
// concurrencia 1 (protege la RAM) procesa lotes de hasta 25 destinatarios con UNA llamada a LibreOffice.
type DocumentService struct {
	repo      ports.CampaignRepo
	files     ports.CampaignFiles
	templates ports.TemplateStore
	render    ports.DocRenderer
	conv      ports.PDFConverter
	queue     ports.TaskQueue
	events    ports.EventPublisher
	locks     *keyedLocks
	work      ports.Workspace
	batchSize int
	now       func() time.Time
}

var _ ports.DocumentUseCase = (*DocumentService)(nil)

// DocumentDeps agrupa las dependencias del servicio.
type DocumentDeps struct {
	Repo      ports.CampaignRepo
	Files     ports.CampaignFiles
	Templates ports.TemplateStore
	Render    ports.DocRenderer
	Converter ports.PDFConverter
	Queue     ports.TaskQueue
	Events    ports.EventPublisher
	Locks     *CampaignService // comparte el candado por campaña con las ediciones
	Workspace ports.Workspace
	BatchSize int
}

func NewDocumentService(d DocumentDeps) *DocumentService {
	if d.BatchSize <= 0 {
		d.BatchSize = 25
	}
	return &DocumentService{repo: d.Repo, files: d.Files, templates: d.Templates, render: d.Render, conv: d.Converter,
		queue: d.Queue, events: d.Events, locks: d.Locks.locks, work: d.Workspace, batchSize: d.BatchSize, now: time.Now}
}

// DocPayload es la tarea doc:render.
type DocPayload struct {
	CampaignID   string   `json:"campaign_id"`
	RecipientIDs []string `json:"recipient_ids"`
}

// DocumentEvent es el evento "document" del stream de la campaña.
type DocumentEvent struct {
	RecipientID string `json:"recipient_id"`
	Fila        int    `json:"fila"`
	Jugador     string `json:"jugador"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	Done        int    `json:"done"`  // filas válidas con documento generado
	Total       int    `json:"total"` // filas válidas
}

func taskID(campaignID string, ids []string) string {
	h := sha1.New()
	for _, id := range ids {
		h.Write([]byte(id + "\n"))
	}
	return fmt.Sprintf("%s:docs:%s", campaignID, hex.EncodeToString(h.Sum(nil))[:12])
}

// Generate encola la generación de los documentos que faltan, en lotes.
func (s *DocumentService) Generate(ctx context.Context, campaignID string) (ports.GenerateResult, error) {
	defer s.locks.lock(campaignID)()
	c, err := s.repo.Get(ctx, campaignID)
	if err != nil {
		return ports.GenerateResult{}, err
	}
	if c.Status == domain.CampaignDocsRunning {
		return ports.GenerateResult{}, ports.ErrGenerationRunning
	}
	if !c.Editable() {
		return ports.GenerateResult{}, ports.ErrCampaignLocked
	}
	recs, err := s.repo.Recipients(ctx, campaignID)
	if err != nil {
		return ports.GenerateResult{}, err
	}
	pend := domain.PendingDocuments(recs)
	res := ports.GenerateResult{Pending: len(pend)}
	if len(pend) == 0 {
		return res, nil
	}

	// Todas las plantillas necesarias deben estar instaladas (mensaje del cliente).
	seen := map[string]bool{}
	for _, r := range pend {
		if seen[r.Plantilla] {
			continue
		}
		seen[r.Plantilla] = true
		if _, err := s.templates.Get(ctx, r.Plantilla); errors.Is(err, ports.ErrTemplateNotFound) {
			return res, input("Falta la plantilla «%s.docx» en la carpeta plantillas.", r.Plantilla)
		} else if err != nil {
			return res, err
		}
	}

	prev := c.Status
	ok, err := s.repo.SetStatus(ctx, campaignID, []domain.CampaignStatus{prev}, domain.CampaignDocsRunning)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, ports.ErrCampaignLocked
	}
	for start := 0; start < len(pend); start += s.batchSize {
		end := min(start+s.batchSize, len(pend))
		ids := make([]string, 0, end-start)
		for _, r := range pend[start:end] {
			ids = append(ids, r.ID)
		}
		payload, _ := json.Marshal(DocPayload{CampaignID: campaignID, RecipientIDs: ids})
		err := s.queue.Enqueue(ctx, ports.Task{Type: ports.TaskDocRender, Payload: payload, Queue: ports.QueueDocs,
			TaskID: taskID(campaignID, ids), MaxRetry: 3})
		if err != nil && !errors.Is(err, ports.ErrDuplicateTask) {
			// No se pudo encolar: se devuelve la campaña a su estado anterior para poder reintentar.
			_, _ = s.repo.SetStatus(ctx, campaignID, []domain.CampaignStatus{domain.CampaignDocsRunning}, prev)
			return res, err
		}
		res.Batches++
	}
	res.Queued = len(pend)
	log.FromContext(ctx).Info("generación de documentos encolada", "campaign_id", campaignID, "propuestas", res.Queued, "lotes", res.Batches)
	return res, nil
}

type docResult struct {
	rec    domain.Recipient
	docx   string
	doc    *domain.Document
	errMsg string
}

func (r *docResult) fail(msg string, now time.Time) {
	r.errMsg = msg
	r.doc = &domain.Document{Error: msg, GeneratedAt: now}
}

// HandleRender es el handler de la tarea doc:render.
func (s *DocumentService) HandleRender(ctx context.Context, raw []byte) error {
	var p DocPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.CampaignID == "" {
		return fmt.Errorf("%w: carga inválida", ports.ErrSkipRetry)
	}
	lg := log.FromContext(ctx).With("campaign_id", p.CampaignID)
	c, err := s.repo.Get(ctx, p.CampaignID)
	if errors.Is(err, ports.ErrCampaignNotFound) {
		return fmt.Errorf("%w: %v", ports.ErrSkipRetry, err)
	}
	if err != nil {
		return err
	}
	recs, err := s.repo.Recipients(ctx, p.CampaignID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range p.RecipientIDs {
		want[id] = true
	}
	var todo []*docResult
	for _, r := range recs {
		if want[r.ID] && r.Estado == domain.RecipientValid && r.Documento == nil { // idempotente: lo ya hecho se salta
			todo = append(todo, &docResult{rec: r})
		}
	}

	now := s.now().UTC()
	if len(todo) > 0 {
		work, cleanup, err := s.work.Open()
		if err != nil {
			return fmt.Errorf("creando la carpeta de trabajo: %w", err)
		}
		defer cleanup()

		if err := s.renderAll(ctx, c, todo, work, now); err != nil {
			return err
		}
	}
	return s.finish(ctx, c, todo, lg)
}

// renderAll: docx en Go -> una sola llamada a LibreOffice -> PDF a la carpeta de la campaña.
func (s *DocumentService) renderAll(ctx context.Context, c domain.Campaign, todo []*docResult, work string, now time.Time) error {
	tplCache := map[string][]byte{}
	var toConvert []string
	for _, r := range todo {
		tpl, ok := tplCache[r.rec.Plantilla]
		if !ok {
			b, err := s.templates.Get(ctx, r.rec.Plantilla)
			if err != nil {
				r.fail(fmt.Sprintf("Falta la plantilla «%s.docx» en la carpeta plantillas.", r.rec.Plantilla), now)
				continue
			}
			tpl, tplCache[r.rec.Plantilla] = b, b
		}
		beca, _ := c.Config.Beca(r.rec.Beca)
		fechas := make([]string, 0, len(beca.Pagos))
		for i := 0; i < len(beca.Pagos) && i < len(c.FechasPago); i++ {
			if d, err := time.Parse("2006-01-02", c.FechasPago[i]); err == nil {
				fechas = append(fechas, d.Format("02/01/2006"))
			}
		}
		out, err := s.render.Render(ctx, tpl, ports.DocumentInput{Recipient: r.rec, Torneo: c.Torneo,
			FechaEmision: now.In(bogota).Format("02/01/2006"), FechasPago: fechas, Config: c.Config})
		var de *ports.DocError
		switch {
		case errors.As(err, &de):
			r.fail(de.Message, now)
			continue
		case err != nil:
			r.fail("Error inesperado al crear el documento: "+err.Error(), now)
			continue
		}
		r.docx = filepath.Join(work, r.rec.ID+".docx")
		if err := s.work.WriteFile(r.docx, out); err != nil {
			return fmt.Errorf("escribiendo el documento: %w", err)
		}
		toConvert = append(toConvert, r.docx)
	}
	if len(toConvert) == 0 {
		return nil
	}

	pdfDir := filepath.Join(work, "pdf")
	err := s.conv.Convert(ctx, toConvert, pdfDir)
	var ce *ports.ConvertError
	failed := map[string]string{}
	switch {
	case err == nil:
	case errors.As(err, &ce):
		for _, n := range ce.Failed {
			failed[n] = "No se pudo convertir el documento a PDF."
		}
	case errors.Is(err, ports.ErrConverterUnavailable):
		for _, r := range todo {
			if r.docx != "" {
				failed[r.rec.ID] = ports.ErrConverterUnavailable.Error()
			}
		}
	default:
		return err // plazo vencido, cancelación…: se reintenta con backoff
	}
	for _, r := range todo {
		if r.docx == "" {
			continue
		}
		if msg, bad := failed[r.rec.ID]; bad {
			r.fail(msg, now)
			continue
		}
		rel, sum, err := s.files.SavePDF(ctx, c, r.rec.ID, filepath.Join(pdfDir, r.rec.ID+".pdf"))
		if err != nil {
			r.fail("No se pudo guardar el PDF: "+err.Error(), now)
			continue
		}
		r.doc = &domain.Document{Path: rel, SHA256: sum, GeneratedAt: now}
	}
	return nil
}

// finish guarda los resultados bajo el candado de la campaña, publica los eventos y, si ya no queda
// nada pendiente, pasa la campaña a DOCS_DONE.
func (s *DocumentService) finish(ctx context.Context, c domain.Campaign, todo []*docResult, lg *slog.Logger) error {
	defer s.locks.lock(c.ID)()
	recs, err := s.repo.Recipients(ctx, c.ID)
	if err != nil {
		return err
	}
	byID := map[string]int{}
	for i, r := range recs {
		byID[r.ID] = i
	}
	var changed []domain.Recipient
	var applied []*docResult
	for _, r := range todo {
		i, ok := byID[r.rec.ID]
		if !ok || r.doc == nil {
			continue
		}
		cur := recs[i]
		// Si la fila cambió mientras se generaba, el documento ya no corresponde a sus datos: se descarta.
		if cur.Raw != r.rec.Raw || cur.Estado != domain.RecipientValid || cur.Documento != nil {
			continue
		}
		recs[i].Documento = r.doc
		changed = append(changed, recs[i])
		applied = append(applied, r)
	}
	if len(changed) > 0 {
		if err := s.repo.SaveRecipients(ctx, c.ID, changed, domain.Summarize(recs)); err != nil {
			return err
		}
	}

	done, total, failedN := 0, 0, 0
	for _, r := range recs {
		if r.Estado == domain.RecipientValid {
			total++
			if r.HasDocument() {
				done++
			} else if r.Documento != nil {
				failedN++
			}
		}
	}
	for _, r := range applied {
		ev := DocumentEvent{RecipientID: r.rec.ID, Fila: r.rec.Fila, Jugador: r.rec.Jugador, OK: r.doc.Error == "", Error: r.doc.Error, Done: done, Total: total}
		if err := s.events.Publish(ctx, c.ID, "document", ev); err != nil {
			lg.Warn("no se pudo publicar el evento del documento", "error", err)
		}
	}
	if len(domain.PendingDocuments(recs)) == 0 {
		if moved, err := s.repo.SetStatus(ctx, c.ID, []domain.CampaignStatus{domain.CampaignDocsRunning}, domain.CampaignDocsDone); err != nil {
			return err
		} else if moved {
			_ = s.events.Publish(ctx, c.ID, "campaign", map[string]any{"status": domain.CampaignDocsDone, "generados": done, "fallidos": failedN, "total": total})
			lg.Info("documentos de la campaña listos", "generados", done, "fallidos", failedN, "total", total)
		}
	}
	return nil
}

// bogota: la fecha de emisión se escribe en la hora de la operación (America/Bogota), no en UTC.
var bogota = func() *time.Location {
	if l, err := time.LoadLocation("America/Bogota"); err == nil {
		return l
	}
	return time.FixedZone("COT", -5*3600)
}()
