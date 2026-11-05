package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// DispatchSettings configura el envío.
type DispatchSettings struct {
	WATemplate    string // plantilla de WhatsApp aprobada (purchase_receipt_3)
	WALanguage    string // es_CO
	EmailDaily    int    // tope diario de correos (Gmail ≈ 500; por defecto 450)
	Allowlist     []string
	StuckAfter    time.Duration // un SENDING más viejo que esto sin resultado queda en revisión (5 min)
	PausedRecheck time.Duration // cada cuánto una tarea de una campaña en pausa vuelve a mirar (60 s)
}

// DispatchDeps agrupa las dependencias.
type DispatchDeps struct {
	Repo       ports.CampaignRepo
	Files      ports.CampaignFiles
	Deliveries ports.DeliveryRepo
	Queue      ports.TaskQueue
	Events     ports.EventPublisher
	WhatsApp   ports.WhatsAppClient // nil = canal deshabilitado
	Email      ports.EmailSender    // nil = canal deshabilitado
	Composer   ports.MailComposer
	Chatwoot   ports.ChatwootClient
	Quota      ports.QuotaStore
	WALimit    ports.Limiter
	EmailLimit ports.Limiter
	Locks      *CampaignService
	Settings   DispatchSettings
}

// DispatchService envía las propuestas y lleva el estado real de cada entrega (SDD §3.5.1 y §4.5-4.8).
type DispatchService struct {
	d     DispatchDeps
	locks *keyedLocks
	now   func() time.Time
	allow map[string]bool
}

var _ ports.DispatchUseCase = (*DispatchService)(nil)

func NewDispatchService(d DispatchDeps) *DispatchService {
	if d.Settings.StuckAfter <= 0 {
		d.Settings.StuckAfter = 5 * time.Minute
	}
	if d.Settings.PausedRecheck <= 0 {
		d.Settings.PausedRecheck = time.Minute
	}
	if d.Settings.EmailDaily <= 0 {
		d.Settings.EmailDaily = 450
	}
	allow := map[string]bool{}
	for _, a := range d.Settings.Allowlist {
		if a = normalizeTarget(a); a != "" {
			allow[a] = true
		}
	}
	return &DispatchService{d: d, locks: d.Locks.locks, now: time.Now, allow: allow}
}

// normalizeTarget unifica teléfonos (solo dígitos, 57 + 10) y correos (minúsculas) para la lista de pruebas.
func normalizeTarget(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "@") {
		return strings.ToLower(s)
	}
	if p, err := domain.NormalizePhone(s); err == nil && p != "" {
		return p
	}
	return ""
}

// ---------------------------------------------------------------------------------------------
// Despacho
// ---------------------------------------------------------------------------------------------

type payload struct {
	CampaignID string `json:"campaign_id"`
	Key        string `json:"key"`
}

func taskFor(ch domain.Channel) (string, string) {
	if ch == domain.ChannelEmail {
		return ports.TaskEmailSend, ports.QueueEmail
	}
	return ports.TaskWASend, ports.QueueWhatsApp
}

// enqueue encola el envío de una entrega. El TaskID lleva el nº de eventos del historial: cada intento
// del operador es una tarea distinta (asynq no admite repetir un TaskID ya archivado).
func (s *DispatchService) enqueue(ctx context.Context, campaignID, key string, ch domain.Channel, attempt int, at time.Time) error {
	typ, queue := taskFor(ch)
	body, _ := json.Marshal(payload{CampaignID: campaignID, Key: key})
	t := ports.Task{Type: typ, Payload: body, Queue: queue, TaskID: fmt.Sprintf("%s#%d", key, attempt), MaxRetry: 5, ProcessAt: at}
	if err := s.d.Queue.Enqueue(ctx, t); err != nil && !errors.Is(err, ports.ErrDuplicateTask) {
		return err
	}
	return nil
}

func (s *DispatchService) channels(requested []string) ([]domain.Channel, error) {
	enabled := map[domain.Channel]bool{domain.ChannelWhatsApp: s.d.WhatsApp != nil, domain.ChannelEmail: s.d.Email != nil}
	var out []domain.Channel
	if len(requested) == 0 {
		for _, ch := range []domain.Channel{domain.ChannelWhatsApp, domain.ChannelEmail} {
			if enabled[ch] {
				out = append(out, ch)
			}
		}
		if len(out) == 0 {
			return nil, input("No hay ningún canal de envío configurado (WhatsApp o correo).")
		}
		return out, nil
	}
	seen := map[domain.Channel]bool{}
	for _, r := range requested {
		ch := domain.Channel(strings.ToLower(strings.TrimSpace(r)))
		if ch != domain.ChannelWhatsApp && ch != domain.ChannelEmail {
			return nil, input("Canal desconocido: %q (use whatsapp o email).", r)
		}
		if !enabled[ch] {
			if ch == domain.ChannelEmail {
				return nil, input("El envío por correo no está configurado: faltan SMTP_USER y SMTP_APP_PASSWORD en el servidor.")
			}
			return nil, input("El envío por WhatsApp no está configurado.")
		}
		if !seen[ch] {
			seen[ch] = true
			out = append(out, ch)
		}
	}
	return out, nil
}

func (s *DispatchService) skipReason(r domain.Recipient, ch domain.Channel) string {
	switch ch {
	case domain.ChannelWhatsApp:
		if !r.HasWhatsApp() {
			return "Sin móvil válido"
		}
		if len(s.allow) > 0 && !s.allow[r.Movil] {
			return "Fuera de la lista de pruebas (DISPATCH_ALLOWLIST)"
		}
	case domain.ChannelEmail:
		if !r.HasEmail() {
			return "Sin correo válido"
		}
		if len(s.allow) > 0 {
			for _, c := range r.Correos {
				if !s.allow[strings.ToLower(c)] {
					return "Fuera de la lista de pruebas (DISPATCH_ALLOWLIST)"
				}
			}
		}
	}
	return ""
}

func providerMessage(err error) string {
	var pe *ports.ProviderError
	if errors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

// Dispatch crea las entregas de las filas válidas con documento y las encola. Es idempotente: una
// entrega que ya existe no se repite (la clave incluye campaña, destinatario, canal y tipo).
func (s *DispatchService) Dispatch(ctx context.Context, campaignID string, in ports.DispatchInput) (ports.DispatchResult, error) {
	defer s.locks.lock(campaignID)()
	var res ports.DispatchResult
	c, err := s.d.Repo.Get(ctx, campaignID)
	if err != nil {
		return res, err
	}
	switch c.Status {
	case domain.CampaignDocsDone:
	case domain.CampaignDocsRunning:
		return res, ports.ErrGenerationRunning
	case domain.CampaignDraft, domain.CampaignReady:
		return res, input("Primero genere las propuestas en PDF.")
	case domain.CampaignPaused:
		return res, ports.ErrCampaignPaused
	default:
		return res, ports.ErrDispatchRunning
	}
	chans, err := s.channels(in.Channels)
	if err != nil {
		return res, err
	}
	recs, err := s.d.Repo.Recipients(ctx, campaignID)
	if err != nil {
		return res, err
	}
	var ready []domain.Recipient
	for _, r := range recs {
		switch {
		case r.CanDispatch():
			ready = append(ready, r)
		case r.Estado == domain.RecipientValid:
			res.NoDocument++ // sin PDF no puede tener entregas (SDD §4.4)
		}
	}
	if len(ready) == 0 {
		return res, input("No hay propuestas listas para enviar: ninguna fila válida tiene su PDF generado.")
	}

	// Comprobación previa: no se encola nada si las credenciales ya no sirven.
	for _, ch := range chans {
		var perr error
		if ch == domain.ChannelWhatsApp {
			perr = s.d.WhatsApp.CheckToken(ctx)
		} else {
			perr = s.d.Email.Verify(ctx)
		}
		if perr != nil {
			who := "WhatsApp"
			if ch == domain.ChannelEmail {
				who = "el correo"
			}
			return res, input("No se puede enviar por %s: %s", who, providerMessage(perr))
		}
	}

	ok, err := s.d.Repo.SetStatus(ctx, campaignID, []domain.CampaignStatus{domain.CampaignDocsDone}, domain.CampaignDispatching)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, ports.ErrDispatchRunning
	}
	now := s.now().UTC()
	for _, r := range ready {
		for _, ch := range chans {
			d := domain.NewDelivery(campaignID, r.ID, ch, domain.KindProposal, now)
			created, err := s.d.Deliveries.Create(ctx, campaignID, d)
			if err != nil {
				return res, err
			}
			if !created {
				res.AlreadyExist++
				continue
			}
			res.Created++
			if reason := s.skipReason(r, ch); reason != "" {
				if _, err := s.d.Deliveries.Transition(ctx, campaignID, domain.Transition{Key: d.Key, To: domain.StatusSkipped, Source: domain.SourceSystem, At: now, Detail: reason}); err != nil {
					return res, err
				}
				res.Skipped++
				continue
			}
			if _, err := s.d.Deliveries.Transition(ctx, campaignID, domain.Transition{Key: d.Key, To: domain.StatusQueued, Source: domain.SourceSystem, At: now}); err != nil {
				return res, err
			}
			if err := s.enqueue(ctx, campaignID, d.Key, ch, 1, time.Time{}); err != nil {
				return res, fmt.Errorf("encolando %s: %w", d.Key, err)
			}
			res.Queued++
		}
	}
	names := make([]string, len(chans))
	for i, ch := range chans {
		names[i] = string(ch)
	}
	_ = s.d.Files.Audit(ctx, c, domain.AuditEvent{Time: now, Type: "campaign.dispatched", CampaignID: campaignID, By: in.By,
		Detail: fmt.Sprintf("canales=%s entregas=%d en_cola=%d omitidas=%d sin_documento=%d", strings.Join(names, ","), res.Created, res.Queued, res.Skipped, res.NoDocument)})
	s.publish(ctx, campaignID, "campaign", map[string]any{"status": domain.CampaignDispatching})
	log.FromContext(ctx).Info("campaña despachada", "campaign_id", campaignID, "creadas", res.Created, "en_cola", res.Queued, "omitidas", res.Skipped)
	s.checkCompletion(ctx, c)
	return res, nil
}

// Retry reenvía lo que falló por motivos no permanentes y lo que quedó en revisión o sin confirmar.
func (s *DispatchService) Retry(ctx context.Context, campaignID string) (ports.RetryResult, error) {
	defer s.locks.lock(campaignID)()
	var res ports.RetryResult
	c, err := s.d.Repo.Get(ctx, campaignID)
	if err != nil {
		return res, err
	}
	switch c.Status {
	case domain.CampaignPaused:
		return res, ports.ErrCampaignPaused
	case domain.CampaignCompleted, domain.CampaignDispatching:
	default:
		return res, input("Esta campaña todavía no se ha despachado.")
	}
	ds, err := s.d.Deliveries.List(ctx, campaignID)
	if err != nil {
		return res, err
	}
	now := s.now().UTC()
	var requeue []domain.Delivery
	for _, d := range ds {
		switch d.Status {
		case domain.StatusFailed:
			if d.LastError != nil && d.LastError.Permanent {
				res.Permanent++
				continue
			}
			requeue = append(requeue, d)
		case domain.StatusNeedsReview, domain.StatusUnconfirmed:
			requeue = append(requeue, d)
		}
	}
	if len(requeue) == 0 {
		return res, nil
	}
	if _, err := s.d.Repo.SetStatus(ctx, campaignID, []domain.CampaignStatus{domain.CampaignCompleted, domain.CampaignDispatching}, domain.CampaignDispatching); err != nil {
		return res, err
	}
	for _, d := range requeue {
		tr, err := s.d.Deliveries.Transition(ctx, campaignID, domain.Transition{Key: d.Key, To: domain.StatusQueued, Source: domain.SourceOperator, At: now, Detail: "reenvío del operador"})
		if err != nil {
			return res, err
		}
		if tr.Outcome != domain.OutcomeApplied {
			continue
		}
		if err := s.enqueue(ctx, campaignID, d.Key, d.Channel, len(d.History)+1, time.Time{}); err != nil {
			return res, err
		}
		res.Requeued++
	}
	s.publish(ctx, campaignID, "campaign", map[string]any{"status": domain.CampaignDispatching})
	return res, nil
}

// Resume reanuda una campaña pausada (p. ej. tras corregir el token).
func (s *DispatchService) Resume(ctx context.Context, campaignID string) error {
	defer s.locks.lock(campaignID)()
	moved, err := s.d.Repo.SetStatus(ctx, campaignID, []domain.CampaignStatus{domain.CampaignPaused}, domain.CampaignDispatching)
	if err != nil {
		return err
	}
	if !moved {
		return input("La campaña no está en pausa.")
	}
	s.publish(ctx, campaignID, "campaign", map[string]any{"status": domain.CampaignDispatching})
	return nil
}

// Snapshot es el estado de los envíos para la pantalla (se consulta cada pocos segundos).
func (s *DispatchService) Snapshot(ctx context.Context, campaignID string) (ports.DeliverySnapshot, error) {
	c, err := s.d.Repo.Get(ctx, campaignID)
	if err != nil {
		return ports.DeliverySnapshot{}, err
	}
	recs, err := s.d.Repo.Recipients(ctx, campaignID)
	if err != nil {
		return ports.DeliverySnapshot{}, err
	}
	ds, err := s.d.Deliveries.List(ctx, campaignID)
	if err != nil {
		return ports.DeliverySnapshot{}, err
	}
	counters, err := s.d.Deliveries.Counters(ctx, campaignID)
	if err != nil {
		return ports.DeliverySnapshot{}, err
	}
	snap := ports.DeliverySnapshot{Status: string(c.Status), Progress: map[string]ports.ChannelProgress{}, Deliveries: []ports.DeliveryView{}}
	for k, n := range counters {
		ch, st, ok := strings.Cut(k, ":")
		if !ok {
			continue
		}
		p := snap.Progress[ch]
		if p.Counts == nil {
			p.Counts = map[string]int{}
		}
		if st == "total" {
			p.Total = n
		} else if n != 0 {
			p.Counts[st] = n
			if domain.IsInFlight(domain.DeliveryStatus(st)) {
				snap.InFlight += n
			}
		}
		snap.Progress[ch] = p
	}
	byID := map[string]domain.Recipient{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	for _, d := range ds {
		_, rid, _, _, err := domain.ParseDeliveryKey(d.Key)
		if err != nil {
			continue
		}
		r := byID[rid]
		v := ports.DeliveryView{Key: d.Key, RecipientID: rid, Fila: r.Fila, Jugador: r.Jugador, Channel: string(d.Channel),
			Status: string(d.Status), Label: domain.StatusLabel(d.Channel, d.Status), Attempts: d.Attempts}
		if n := len(d.History); n > 0 {
			v.At = d.History[n-1].At
			if d.Status == domain.StatusSkipped {
				v.Error = d.History[n-1].Detail
			}
		}
		if d.LastError != nil && (d.Status == domain.StatusFailed || d.Status == domain.StatusRetryWait || d.Status == domain.StatusNeedsReview) {
			v.Error, v.Permanent = d.LastError.Message, d.LastError.Permanent
		}
		snap.Deliveries = append(snap.Deliveries, v)
	}
	sort.SliceStable(snap.Deliveries, func(i, j int) bool {
		a, b := snap.Deliveries[i], snap.Deliveries[j]
		if a.Fila != b.Fila {
			return a.Fila < b.Fila
		}
		return a.Channel < b.Channel
	})
	return snap, nil
}

func (s *DispatchService) publish(ctx context.Context, campaignID, typ string, data any) {
	if err := s.d.Events.Publish(ctx, campaignID, typ, data); err != nil {
		log.FromContext(ctx).Warn("no se pudo publicar el evento", "tipo", typ, "error", err)
	}
}

// checkCompletion pasa la campaña a COMPLETED cuando ya no queda nada en curso.
func (s *DispatchService) checkCompletion(ctx context.Context, c domain.Campaign) {
	counters, err := s.d.Deliveries.Counters(ctx, c.ID)
	if err != nil {
		return
	}
	for k, n := range counters {
		if _, st, ok := strings.Cut(k, ":"); ok && n > 0 && domain.IsInFlight(domain.DeliveryStatus(st)) {
			return
		}
	}
	if moved, err := s.d.Repo.SetStatus(ctx, c.ID, []domain.CampaignStatus{domain.CampaignDispatching}, domain.CampaignCompleted); err == nil && moved {
		s.publish(ctx, c.ID, "campaign", map[string]any{"status": domain.CampaignCompleted})
		log.FromContext(ctx).Info("campaña completada", "campaign_id", c.ID)
	}
}

// ---------------------------------------------------------------------------------------------
// Workers
// ---------------------------------------------------------------------------------------------

// run es el contexto de una tarea de envío ya validada.
type run struct {
	c   domain.Campaign
	rec domain.Recipient
	d   domain.Delivery
	key string
	ch  domain.Channel
	lg  *slog.Logger
}

// begin decide si la tarea debe enviar. Devuelve nil si no hay nada que hacer (ya enviada, en pausa…).
// NO cambia a SENDING: eso se hace justo antes de llamar al proveedor (tras esperar el ritmo).
func (s *DispatchService) begin(ctx context.Context, raw []byte, want domain.Channel) (*run, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil || p.CampaignID == "" || p.Key == "" {
		return nil, fmt.Errorf("%w: carga inválida", ports.ErrSkipRetry)
	}
	cid, rid, ch, _, err := domain.ParseDeliveryKey(p.Key)
	if err != nil || cid != p.CampaignID || ch != want {
		return nil, fmt.Errorf("%w: clave inconsistente", ports.ErrSkipRetry)
	}
	lg := log.FromContext(ctx).With("campaign_id", cid, "delivery_key", p.Key)
	c, err := s.d.Repo.Get(ctx, cid)
	if errors.Is(err, ports.ErrCampaignNotFound) {
		return nil, fmt.Errorf("%w: %v", ports.ErrSkipRetry, err)
	}
	if err != nil {
		return nil, err
	}
	d, err := s.getDelivery(ctx, cid, p.Key)
	if err != nil {
		return nil, err
	}
	// En pausa (o cancelada): la tarea se vuelve a mirar en un minuto, sin gastar reintentos.
	if c.Status == domain.CampaignPaused || c.Status == domain.CampaignCancelled {
		if err := s.enqueue(ctx, cid, p.Key, ch, int(s.now().Unix()), s.now().Add(s.d.Settings.PausedRecheck)); err != nil {
			return nil, err
		}
		return nil, nil
	}
	switch d.Status {
	case domain.StatusQueued, domain.StatusRetryWait:
	case domain.StatusSending:
		// Un SENDING viejo es un proceso que murió después de llamar al proveedor: NO se reenvía (podría
		// duplicarse): queda en revisión para que el operador decida.
		if n := len(d.History); n > 0 && s.now().Sub(d.History[n-1].At) > s.d.Settings.StuckAfter {
			lg.Warn("entrega atascada en SENDING: pasa a revisión")
			if _, err := s.d.Deliveries.Transition(ctx, cid, domain.Transition{Key: p.Key, To: domain.StatusNeedsReview, Source: domain.SourceSystem, At: s.now(),
				Detail: "el envío quedó a medias: no se sabe si salió", Error: &domain.ProviderError{Code: "atascada", Message: "El envío quedó a medias y no se sabe si el mensaje salió. Revíselo antes de reenviar."}}); err != nil {
				return nil, err
			}
			s.checkCompletion(ctx, c)
			return nil, nil
		}
		return nil, errors.New("la entrega está siendo procesada por otro worker") // se reintenta con backoff
	default:
		return nil, nil // ya aceptada, fallida, omitida…: idempotente
	}
	recs, err := s.d.Repo.Recipients(ctx, cid)
	if err != nil {
		return nil, err
	}
	var rec *domain.Recipient
	for i := range recs {
		if recs[i].ID == rid {
			rec = &recs[i]
		}
	}
	r := &run{c: c, d: d, key: p.Key, ch: ch, lg: lg}
	if rec == nil || !rec.CanDispatch() {
		s.fail(ctx, r, &ports.ProviderError{Action: ports.ActionFail, Code: "sin-documento", Message: "La propuesta en PDF ya no está disponible."}, false)
		return nil, nil
	}
	r.rec = *rec
	return r, nil
}

func (s *DispatchService) getDelivery(ctx context.Context, cid, key string) (domain.Delivery, error) {
	d, err := s.d.Deliveries.Get(ctx, cid, key)
	if errors.Is(err, ports.ErrDeliveryNotFound) {
		return d, fmt.Errorf("%w: %v", ports.ErrSkipRetry, err)
	}
	return d, err
}

// toSending marca SENDING con la hora: es el registro de «a punto de llamar al proveedor».
func (s *DispatchService) toSending(ctx context.Context, r *run) (bool, error) {
	tr, err := s.d.Deliveries.Transition(ctx, r.c.ID, domain.Transition{Key: r.key, To: domain.StatusSending, Source: domain.SourceWorker, At: s.now()})
	if err != nil {
		return false, err
	}
	return tr.Outcome == domain.OutcomeApplied, nil
}

func asDomainError(pe *ports.ProviderError) *domain.ProviderError {
	return &domain.ProviderError{Code: pe.Code, Message: pe.Message, Permanent: pe.Permanent(), Raw: pe.Raw}
}

func (s *DispatchService) transition(ctx context.Context, r *run, to domain.DeliveryStatus, pe *ports.ProviderError, providerID string) error {
	t := domain.Transition{Key: r.key, To: to, Source: domain.SourceWorker, At: s.now(), ProviderID: providerID}
	if pe != nil {
		t.Error = asDomainError(pe)
		t.Detail = pe.Message
	}
	_, err := s.d.Deliveries.Transition(ctx, r.c.ID, t)
	return err
}

// fail registra un fallo definitivo (y mira si la campaña ya terminó).
func (s *DispatchService) fail(ctx context.Context, r *run, pe *ports.ProviderError, fromSending bool) {
	if !fromSending { // QUEUED/RETRY_WAIT no pueden ir directo a FAILED: pasan por SENDING
		if ok, err := s.toSending(ctx, r); err != nil || !ok {
			return
		}
	}
	pe2 := *pe
	pe2.Action = ports.ActionFail
	if err := s.transition(ctx, r, domain.StatusFailed, &pe2, ""); err != nil {
		r.lg.Error("no se pudo registrar el fallo", "error", err)
		return
	}
	r.lg.Warn("entrega fallida", "codigo", pe.Code, "motivo", pe.Message)
	s.publishDelivery(ctx, r, domain.StatusFailed, pe.Message)
	s.checkCompletion(ctx, r.c)
}

func (s *DispatchService) publishDelivery(ctx context.Context, r *run, st domain.DeliveryStatus, detail string) {
	s.publish(ctx, r.c.ID, "delivery", map[string]any{"key": r.key, "channel": r.ch, "status": st, "label": domain.StatusLabel(r.ch, st),
		"recipient_id": r.rec.ID, "jugador": r.rec.Jugador, "detail": detail})
}

// outcome aplica el resultado del proveedor. Devuelve el error que debe ver la cola (nil = tarea terminada).
func (s *DispatchService) outcome(ctx context.Context, r *run, err error, providerID string) error {
	if err == nil {
		if terr := s.transition(ctx, r, domain.StatusAccepted, nil, providerID); terr != nil {
			// El proveedor ya aceptó: NO se reintenta. El SENDING pendiente pasará a revisión.
			r.lg.Error("aceptado por el proveedor pero no se pudo registrar", "error", terr)
			return fmt.Errorf("%w: %v", ports.ErrSkipRetry, terr)
		}
		r.lg.Info("entrega aceptada", "canal", r.ch)
		s.publishDelivery(ctx, r, domain.StatusAccepted, "")
		s.afterAccepted(ctx, r, providerID)
		s.checkCompletion(ctx, r.c)
		return nil
	}
	var pe *ports.ProviderError
	if !errors.As(err, &pe) {
		// Error propio (p. ej. el PDF no se pudo leer): no se llamó al proveedor, es seguro reintentar.
		pe = &ports.ProviderError{Action: ports.ActionRetry, Code: "interno", Message: "Error interno preparando el envío. Se reintentará.", Raw: err.Error()}
	}
	switch pe.Action {
	case ports.ActionFail:
		if terr := s.transition(ctx, r, domain.StatusFailed, pe, ""); terr != nil {
			return terr
		}
		r.lg.Warn("entrega fallida", "codigo", pe.Code, "motivo", pe.Message, "detalle", pe.Raw)
		s.publishDelivery(ctx, r, domain.StatusFailed, pe.Message)
		s.checkCompletion(ctx, r.c)
		return nil
	case ports.ActionReview:
		if terr := s.transition(ctx, r, domain.StatusNeedsReview, pe, ""); terr != nil {
			return terr
		}
		r.lg.Warn("entrega en revisión", "codigo", pe.Code, "detalle", pe.Raw)
		s.publishDelivery(ctx, r, domain.StatusNeedsReview, pe.Message)
		s.checkCompletion(ctx, r.c)
		return nil
	case ports.ActionPause:
		if terr := s.transition(ctx, r, domain.StatusRetryWait, pe, ""); terr != nil {
			return terr
		}
		if moved, _ := s.d.Repo.SetStatus(ctx, r.c.ID, []domain.CampaignStatus{domain.CampaignDispatching}, domain.CampaignPaused); moved {
			r.lg.Error("campaña en pausa", "codigo", pe.Code, "motivo", pe.Message)
			s.publish(ctx, r.c.ID, "campaign", map[string]any{"status": domain.CampaignPaused, "reason": pe.Message})
		}
		return s.enqueue(ctx, r.c.ID, r.key, r.ch, int(s.now().Unix()), s.now().Add(s.d.Settings.PausedRecheck))
	case ports.ActionDefer:
		if terr := s.transition(ctx, r, domain.StatusRetryWait, pe, ""); terr != nil {
			return terr
		}
		next := nextMorning(s.now())
		r.lg.Warn("cuota diaria agotada: se reprograma", "para", next)
		s.publish(ctx, r.c.ID, "notice", map[string]any{"message": "Límite diario de correo alcanzado; los pendientes continúan mañana a las 08:00.", "resume_at": next})
		return s.enqueue(ctx, r.c.ID, r.key, r.ch, int(s.now().Unix()), next)
	default: // ActionRetry
		if ports.AttemptFrom(ctx).IsLast() {
			pe2 := *pe
			pe2.Message += " Se agotaron los reintentos."
			if terr := s.transition(ctx, r, domain.StatusFailed, &pe2, ""); terr != nil {
				return terr
			}
			s.publishDelivery(ctx, r, domain.StatusFailed, pe2.Message)
			s.checkCompletion(ctx, r.c)
			return nil
		}
		if terr := s.transition(ctx, r, domain.StatusRetryWait, pe, ""); terr != nil {
			return terr
		}
		s.publishDelivery(ctx, r, domain.StatusRetryWait, pe.Message)
		return err // asynq reintenta con backoff
	}
}

// nextMorning: mañana a las 08:00 hora de Bogotá.
func nextMorning(now time.Time) time.Time {
	b := now.In(bogota)
	return time.Date(b.Year(), b.Month(), b.Day()+1, 8, 0, 0, 0, bogota)
}

// afterAccepted deja constancia en Chatwoot (no bloquea la entrega: tarea aparte en la cola side).
func (s *DispatchService) afterAccepted(ctx context.Context, r *run, providerID string) {
	if s.d.Chatwoot == nil || r.rec.Movil == "" {
		return
	}
	beca := domain.BecaLabel(r.rec.Beca)
	var text string
	if r.ch == domain.ChannelWhatsApp {
		text = fmt.Sprintf("📄 Propuesta de beca %s enviada por plantilla de WhatsApp (aceptada por Meta, wamid %s) · %s", beca, providerID, r.c.Torneo)
	} else {
		text = fmt.Sprintf("✉️ Propuesta de beca %s enviada por correo a %s (aceptada por el servidor) · %s", beca, strings.Join(r.rec.Correos, ", "), r.c.Torneo)
	}
	body, _ := json.Marshal(struct {
		CampaignID  string `json:"campaign_id"`
		RecipientID string `json:"recipient_id"`
		Text        string `json:"text"`
	}{r.c.ID, r.rec.ID, text})
	if err := s.d.Queue.Enqueue(ctx, ports.Task{Type: ports.TaskNote, Payload: body, Queue: ports.QueueSide, MaxRetry: 5,
		TaskID: fmt.Sprintf("%s:note:%s:%s", r.c.ID, r.rec.ID, r.ch)}); err != nil && !errors.Is(err, ports.ErrDuplicateTask) {
		r.lg.Warn("no se pudo encolar la nota de Chatwoot", "error", err)
	}
}

// HandleNote publica la nota privada en Chatwoot. Un fallo no afecta el estado de la entrega.
func (s *DispatchService) HandleNote(ctx context.Context, raw []byte) error {
	var p struct {
		CampaignID  string `json:"campaign_id"`
		RecipientID string `json:"recipient_id"`
		Text        string `json:"text"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.CampaignID == "" {
		return fmt.Errorf("%w: carga inválida", ports.ErrSkipRetry)
	}
	recs, err := s.d.Repo.Recipients(ctx, p.CampaignID)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.ID == p.RecipientID && r.Movil != "" && s.d.Chatwoot != nil {
			return s.d.Chatwoot.RegisterNote(ctx, r.Movil, r.Jugador, p.Text)
		}
	}
	return nil
}

// ensureMedia devuelve el media_id del PDF en Meta, subiéndolo si falta o venció (dura 30 días).
func (s *DispatchService) ensureMedia(ctx context.Context, r *run, filename string) (string, error) {
	now := s.now().UTC()
	if doc := r.rec.Documento; doc != nil && doc.MediaValid(now) {
		return doc.MediaID, nil
	}
	pdf, err := s.d.Files.ReadPDF(ctx, r.c, r.rec.Documento.Path)
	if err != nil {
		return "", fmt.Errorf("leyendo el PDF: %w", err)
	}
	id, err := s.d.WhatsApp.UploadMedia(ctx, filename, pdf)
	if err != nil {
		return "", err
	}
	// Se guarda para los reintentos (y para el recordatorio, que reutiliza el mismo PDF). Se relee la
	// fila bajo el candado: otra tarea pudo guardar su propio cambio mientras se subía.
	func() {
		defer s.locks.lock(r.c.ID)()
		recs, err := s.d.Repo.Recipients(ctx, r.c.ID)
		if err != nil {
			return
		}
		for i := range recs {
			if recs[i].ID == r.rec.ID && recs[i].Documento != nil && recs[i].Raw == r.rec.Raw {
				recs[i].Documento.MediaID, recs[i].Documento.MediaExpiresAt = id, now.Add(29*24*time.Hour)
				_ = s.d.Repo.SaveRecipients(ctx, r.c.ID, []domain.Recipient{recs[i]}, domain.Summarize(recs))
			}
		}
	}()
	return id, nil
}

func (s *DispatchService) visibleName(ctx context.Context, r *run) string {
	recs, err := s.d.Repo.Recipients(ctx, r.c.ID)
	if err != nil {
		return domain.SafeFileName(r.rec.Jugador, 90) + ".pdf"
	}
	return domain.VisibleFileNames(recs)[r.rec.ID]
}

// HandleWhatsApp envía la propuesta por la plantilla aprobada, con el PDF subido como medio.
func (s *DispatchService) HandleWhatsApp(ctx context.Context, raw []byte) error {
	if s.d.WhatsApp == nil {
		return fmt.Errorf("%w: WhatsApp no está configurado", ports.ErrSkipRetry)
	}
	r, err := s.begin(ctx, raw, domain.ChannelWhatsApp)
	if err != nil || r == nil {
		return err
	}
	if err := s.d.WALimit.Wait(ctx); err != nil { // el ritmo se espera ANTES de marcar SENDING
		return err
	}
	if ok, err := s.toSending(ctx, r); err != nil || !ok {
		return err
	}
	name := s.visibleName(ctx, r)
	mediaID, err := s.ensureMedia(ctx, r, name)
	if err != nil {
		return s.outcome(ctx, r, err, "")
	}
	beca := strings.TrimSuffix(domain.BecaLabel(r.rec.Beca), "%")
	wamid, err := s.d.WhatsApp.SendTemplate(ctx, r.rec.Movil, ports.TemplateMessage{
		Name: s.d.Settings.WATemplate, Language: s.d.Settings.WALanguage, MediaID: mediaID, DocFilename: name,
		BodyParams: []string{domain.ProperName(r.rec.Acudiente), beca, domain.ProperName(r.rec.Jugador)},
	})
	if err != nil {
		var pe *ports.ProviderError
		if errors.As(err, &pe) && pe.Code == "131053" { // el medio venció o está dañado: se vuelve a subir en el reintento
			s.forgetMedia(ctx, r)
		}
	}
	return s.outcome(ctx, r, err, wamid)
}

func (s *DispatchService) forgetMedia(ctx context.Context, r *run) {
	defer s.locks.lock(r.c.ID)()
	recs, err := s.d.Repo.Recipients(ctx, r.c.ID)
	if err != nil {
		return
	}
	for i := range recs {
		if recs[i].ID == r.rec.ID && recs[i].Documento != nil {
			recs[i].Documento.MediaID = ""
			_ = s.d.Repo.SaveRecipients(ctx, r.c.ID, []domain.Recipient{recs[i]}, domain.Summarize(recs))
		}
	}
}

// HandleEmail envía la propuesta por correo con el PDF adjunto.
func (s *DispatchService) HandleEmail(ctx context.Context, raw []byte) error {
	if s.d.Email == nil {
		return fmt.Errorf("%w: el correo no está configurado", ports.ErrSkipRetry)
	}
	r, err := s.begin(ctx, raw, domain.ChannelEmail)
	if err != nil || r == nil {
		return err
	}
	// Cuota diaria (Gmail ≈ 500/día): al agotarse se reprograma para mañana SIN gastar reintentos.
	day := s.now().In(bogota).Format("20060102")
	ok, err := s.d.Quota.Take(ctx, day, s.d.Settings.EmailDaily)
	if err != nil {
		return err
	}
	if !ok {
		next := nextMorning(s.now())
		s.publish(ctx, r.c.ID, "notice", map[string]any{"message": "Límite diario de correo alcanzado; los pendientes continúan mañana a las 08:00.", "resume_at": next})
		return s.enqueue(ctx, r.c.ID, r.key, r.ch, int(s.now().Unix()), next)
	}
	if err := s.d.EmailLimit.Wait(ctx); err != nil {
		return err
	}
	if ok, err := s.toSending(ctx, r); err != nil || !ok {
		return err
	}
	name := s.visibleName(ctx, r)
	pdf, err := s.d.Files.ReadPDF(ctx, r.c, r.rec.Documento.Path)
	if err != nil {
		return s.outcome(ctx, r, fmt.Errorf("leyendo el PDF: %w", err), "")
	}
	msg := s.d.Composer.Compose(ports.ProposalMail{To: r.rec.Correos, Acudiente: r.rec.Acudiente, Jugador: r.rec.Jugador, Beca: r.rec.Beca,
		Torneo: r.c.Torneo, FileName: name, PDF: pdf, DeliveryKey: r.key})
	id, err := s.d.Email.Send(ctx, msg)
	return s.outcome(ctx, r, err, id)
}
