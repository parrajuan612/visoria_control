package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// ---- simulados ----

type memDeliveries struct {
	mu       sync.Mutex
	m        map[string]*domain.Delivery
	counters map[string]int
}

func newMemDeliveries() *memDeliveries {
	return &memDeliveries{m: map[string]*domain.Delivery{}, counters: map[string]int{}}
}

func (r *memDeliveries) Create(_ context.Context, _ string, d domain.Delivery) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[d.Key]; ok {
		return false, nil
	}
	c := d
	r.m[d.Key] = &c
	r.counters[string(d.Channel)+":total"]++
	r.counters[string(d.Channel)+":"+string(d.Status)]++
	return true, nil
}
func (r *memDeliveries) Get(_ context.Context, _, key string) (domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.m[key]; ok {
		return *d, nil
	}
	return domain.Delivery{}, ports.ErrDeliveryNotFound
}

// Transition usa domain.Apply, que el cruce Go↔Lua demostró idéntico al script de Redis.
func (r *memDeliveries) Transition(_ context.Context, _ string, t domain.Transition) (ports.TransitionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[t.Key]
	if !ok {
		return ports.TransitionResult{}, ports.ErrDeliveryNotFound
	}
	out, prev := d.Apply(t)
	if out == domain.OutcomeApplied {
		r.counters[string(d.Channel)+":"+string(prev)]--
		r.counters[string(d.Channel)+":"+string(d.Status)]++
	}
	return ports.TransitionResult{Outcome: out, Previous: prev}, nil
}
func (r *memDeliveries) List(context.Context, string) ([]domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Delivery
	for _, d := range r.m {
		out = append(out, *d)
	}
	return out, nil
}
func (r *memDeliveries) Counters(context.Context, string) (map[string]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for k, v := range r.counters {
		out[k] = v
	}
	return out, nil
}

type waCall struct {
	To  string
	Msg ports.TemplateMessage
}

type fakeWA struct {
	mu        sync.Mutex
	uploads   []string
	sends     []waCall
	checkErr  error
	uploadErr error
	sendErr   map[string]error // por destino
	sendErrFn func(n int) error
}

func (f *fakeWA) UploadMedia(_ context.Context, name string, pdf []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadErr != nil {
		return "", f.uploadErr
	}
	f.uploads = append(f.uploads, name)
	return fmt.Sprintf("MEDIA%d", len(f.uploads)), nil
}
func (f *fakeWA) SendTemplate(_ context.Context, to string, m ports.TemplateMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, waCall{to, m})
	if e := f.sendErr[to]; e != nil {
		return "", e
	}
	if f.sendErrFn != nil {
		if e := f.sendErrFn(len(f.sends)); e != nil {
			return "", e
		}
	}
	return fmt.Sprintf("wamid.%d", len(f.sends)), nil
}
func (f *fakeWA) CheckToken(context.Context) error { return f.checkErr }

type fakeMail struct {
	mu      sync.Mutex
	sent    []ports.EmailMessage
	verify  error
	sendErr error
}

func (f *fakeMail) Send(_ context.Context, m ports.EmailMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return fmt.Sprintf("<m%d@x>", len(f.sent)), nil
}
func (f *fakeMail) Verify(context.Context) error { return f.verify }

type fakeComposer struct{}

func (fakeComposer) Compose(p ports.ProposalMail) ports.EmailMessage {
	return ports.EmailMessage{To: p.To, Subject: "Propuesta " + p.Jugador, Headers: map[string]string{"X-Visoria-Delivery": p.DeliveryKey},
		Attachments: []ports.EmailAttachment{{Filename: p.FileName, Data: p.PDF}}}
}

type fakeQuota struct {
	limit, used int
	err         error
}

func (q *fakeQuota) Take(context.Context, string, int) (bool, error) {
	if q.err != nil {
		return false, q.err
	}
	if q.used >= q.limit {
		return false, nil
	}
	q.used++
	return true, nil
}

type noLimit struct{}

func (noLimit) Wait(context.Context) error { return nil }

type fakeChatwoot struct {
	mu    sync.Mutex
	notes []string
	err   error
}

func (c *fakeChatwoot) RegisterNote(_ context.Context, phone, name, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.notes = append(c.notes, phone+"|"+name+"|"+content)
	return nil
}

// ---- entorno ----

type dispEnv struct {
	doc   docEnv
	svc   *DispatchService
	deliv *memDeliveries
	wa    *fakeWA
	mail  *fakeMail
	quota *fakeQuota
	chat  *fakeChatwoot
	now   time.Time
}

// Campaña con 5 filas válidas y su PDF ya generado (DOCS_DONE), lista para despachar.
func newDispEnv(t *testing.T, mut func(*DispatchDeps)) *dispEnv {
	t.Helper()
	d := newDocEnv(t, 25)
	// Cada jugador con su propio móvil y correo (el 1.º es el número de prueba). Jugador 3 sin móvil y
	// Jugador 4 sin correo: sus canales faltantes quedan OMITIDOS.
	for i := 0; i < 5; i++ {
		rec := d.camp.repo.recs[d.cid][d.recs[i].ID]
		rec.Movil, rec.Correos = fmt.Sprintf("57320000000%d", i), []string{fmt.Sprintf("j%d@b.co", i+1)}
		if i == 0 {
			rec.Movil, rec.Correos = "573209323449", []string{"a@b.co"}
		}
		if i == 2 {
			rec.Movil = ""
		}
		if i == 3 {
			rec.Correos = nil
		}
		d.camp.repo.recs[d.cid][rec.ID] = rec
	}
	if _, err := d.svc.Generate(context.Background(), d.cid); err != nil {
		t.Fatal(err)
	}
	d.runAll(t)
	if d.status() != domain.CampaignDocsDone {
		t.Fatalf("la campaña debía quedar con documentos: %s", d.status())
	}
	e := &dispEnv{doc: d, deliv: newMemDeliveries(), wa: &fakeWA{sendErr: map[string]error{}}, mail: &fakeMail{}, quota: &fakeQuota{limit: 100}, chat: &fakeChatwoot{},
		now: time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)}
	deps := DispatchDeps{Repo: d.camp.repo, Files: d.camp.files, Deliveries: e.deliv, Queue: d.queue, Events: d.events, WhatsApp: e.wa, Email: e.mail,
		Composer: fakeComposer{}, Chatwoot: e.chat, PDFLink: func(c, r string) string { return "https://x.test/pdf/" + c + "/" + r }, Quota: e.quota, WALimit: noLimit{}, EmailLimit: noLimit{}, Locks: d.camp.svc,
		Settings: DispatchSettings{WATemplate: "purchase_receipt_3", WALanguage: "es_CO", EmailDaily: 100}}
	if mut != nil {
		mut(&deps)
	}
	e.svc = NewDispatchService(deps)
	e.svc.now = func() time.Time { return e.now }
	d.queue.tasks = nil // se descartan las tareas de documentos
	d.queue.seen = nil
	return e
}

func (e dispEnv) status() domain.CampaignStatus { return e.doc.status() }

func (e dispEnv) dispatch(t *testing.T, chans ...string) ports.DispatchResult {
	t.Helper()
	res, err := e.svc.Dispatch(context.Background(), e.doc.cid, ports.DispatchInput{Channels: chans, By: "operador"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	return res
}

func (e dispEnv) delivery(t *testing.T, jugador string, ch domain.Channel) domain.Delivery {
	t.Helper()
	for _, r := range e.doc.recipients(t) {
		if r.Jugador == jugador {
			d, err := e.deliv.Get(context.Background(), e.doc.cid, domain.NewDeliveryKey(e.doc.cid, r.ID, ch, domain.KindProposal))
			if err != nil {
				t.Fatalf("%s/%s: %v", jugador, ch, err)
			}
			return d
		}
	}
	t.Fatalf("no existe %s", jugador)
	return domain.Delivery{}
}

// run ejecuta las tareas encoladas del tipo indicado (las nuevas que se encolen durante la ejecución se ignoran).
func (e dispEnv) run(t *testing.T, typ string) []error {
	t.Helper()
	tasks := append([]ports.Task(nil), e.doc.queue.tasks...)
	e.doc.queue.tasks = nil
	var errs []error
	for _, task := range tasks {
		if task.Type != typ {
			e.doc.queue.tasks = append(e.doc.queue.tasks, task)
			continue
		}
		var err error
		switch typ {
		case ports.TaskWASend:
			err = e.svc.HandleWhatsApp(context.Background(), task.Payload)
		case ports.TaskEmailSend:
			err = e.svc.HandleEmail(context.Background(), task.Payload)
		case ports.TaskNote:
			err = e.svc.HandleNote(context.Background(), task.Payload)
		}
		errs = append(errs, err)
	}
	return errs
}

func noErrs(t *testing.T, errs []error) {
	t.Helper()
	for _, e := range errs {
		if e != nil {
			t.Fatalf("la tarea falló: %v", e)
		}
	}
}

// ---- despacho ----

func TestDespachoCreaYEncolaLasEntregas(t *testing.T) {
	e := newDispEnv(t, nil)
	res := e.dispatch(t)
	// 5 filas × 2 canales = 10 entregas; sin móvil (1) y sin correo (1) quedan omitidas.
	if res.Created != 10 || res.Queued != 8 || res.Skipped != 2 || res.NoDocument != 0 || res.AlreadyExist != 0 {
		t.Fatalf("resultado: %+v", res)
	}
	var wa, mail int
	for _, task := range e.doc.queue.tasks {
		switch task.Type {
		case ports.TaskWASend:
			wa++
			if task.Queue != ports.QueueWhatsApp {
				t.Errorf("cola de WhatsApp: %+v", task)
			}
		case ports.TaskEmailSend:
			mail++
			if task.Queue != ports.QueueEmail {
				t.Errorf("cola de correo: %+v", task)
			}
		}
		if !strings.Contains(task.TaskID, ":proposal#1") || task.MaxRetry != 5 {
			t.Errorf("tarea: %+v", task)
		}
	}
	if wa != 4 || mail != 4 {
		t.Errorf("tareas wa=%d correo=%d", wa, mail)
	}
	if e.status() != domain.CampaignDispatching {
		t.Errorf("estado: %s", e.status())
	}
	if d := e.delivery(t, "Jugador 3", domain.ChannelWhatsApp); d.Status != domain.StatusSkipped || d.History[len(d.History)-1].Detail != "Sin móvil válido" {
		t.Errorf("sin móvil: %+v", d)
	}
	if d := e.delivery(t, "Jugador 4", domain.ChannelEmail); d.Status != domain.StatusSkipped || d.History[len(d.History)-1].Detail != "Sin correo válido" {
		t.Errorf("sin correo: %+v", d)
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusQueued {
		t.Errorf("en cola: %+v", d)
	}
	// Las filas inválidas u omitidas no tienen entregas.
	if len(e.deliv.m) != 10 {
		t.Errorf("entregas: %d", len(e.deliv.m))
	}
	// La auditoría registra el despacho.
	last := e.doc.camp.files.events[len(e.doc.camp.files.events)-1]
	if last.Type != "campaign.dispatched" || last.By != "operador" || !strings.Contains(last.Detail, "en_cola=8") {
		t.Errorf("auditoría: %+v", last)
	}
	// Idempotente: no se puede despachar dos veces.
	if _, err := e.svc.Dispatch(context.Background(), e.doc.cid, ports.DispatchInput{}); !errors.Is(err, ports.ErrDispatchRunning) {
		t.Errorf("segundo despacho: %v", err)
	}
}

func TestDespachoUnSoloCanal(t *testing.T) {
	e := newDispEnv(t, nil)
	res := e.dispatch(t, "whatsapp")
	if res.Created != 5 || res.Queued != 4 || res.Skipped != 1 {
		t.Errorf("solo WhatsApp: %+v", res)
	}
	for _, task := range e.doc.queue.tasks {
		if task.Type == ports.TaskEmailSend {
			t.Error("no debe encolar correos")
		}
	}
}

func TestDespachoPrecondiciones(t *testing.T) {
	ctx := context.Background()
	var ie *ports.InputError
	// Campaña sin documentos → mensaje claro.
	e := newDispEnv(t, nil)
	cc := e.doc.camp.repo.camps[e.doc.cid]
	cc.Status = domain.CampaignDraft
	e.doc.camp.repo.camps[e.doc.cid] = cc
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "Primero genere") {
		t.Errorf("sin documentos: %v", err)
	}
	cc.Status = domain.CampaignDocsRunning
	e.doc.camp.repo.camps[e.doc.cid] = cc
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{}); !errors.Is(err, ports.ErrGenerationRunning) {
		t.Errorf("generando: %v", err)
	}
	cc.Status = domain.CampaignPaused
	e.doc.camp.repo.camps[e.doc.cid] = cc
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{}); !errors.Is(err, ports.ErrCampaignPaused) {
		t.Errorf("pausada: %v", err)
	}
	if _, err := e.svc.Dispatch(ctx, "nope", ports.DispatchInput{}); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Errorf("inexistente: %v", err)
	}

	// Canal desconocido o no configurado.
	e = newDispEnv(t, func(d *DispatchDeps) { d.Email = nil })
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{Channels: []string{"sms"}}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "Canal desconocido") {
		t.Errorf("canal desconocido: %v", err)
	}
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{Channels: []string{"email"}}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "SMTP_USER") {
		t.Errorf("correo no configurado: %v", err)
	}
	// Sin indicar canales usa solo los configurados.
	res := e.dispatch(t)
	if res.Created != 5 {
		t.Errorf("por defecto solo WhatsApp: %+v", res)
	}
	e = newDispEnv(t, func(d *DispatchDeps) { d.Email, d.WhatsApp = nil, nil })
	if _, err := e.svc.Dispatch(ctx, e.doc.cid, ports.DispatchInput{}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "ningún canal") {
		t.Errorf("sin canales: %v", err)
	}
}

// Si las credenciales ya no sirven, NO se encola nada ni cambia el estado.
func TestDespachoVerificaLasCredencialesAntes(t *testing.T) {
	e := newDispEnv(t, nil)
	e.wa.checkErr = &ports.ProviderError{Action: ports.ActionPause, Code: "190", Message: "El token de WhatsApp no es válido o venció."}
	var ie *ports.InputError
	if _, err := e.svc.Dispatch(context.Background(), e.doc.cid, ports.DispatchInput{}); !errors.As(err, &ie) || ie.Message != "No se puede enviar por WhatsApp: El token de WhatsApp no es válido o venció." {
		t.Errorf("token: %v", err)
	}
	e.wa.checkErr = nil
	e.mail.verify = &ports.ProviderError{Action: ports.ActionPause, Message: "El servidor de correo no aceptó la cuenta."}
	if _, err := e.svc.Dispatch(context.Background(), e.doc.cid, ports.DispatchInput{}); !errors.As(err, &ie) || !strings.HasPrefix(ie.Message, "No se puede enviar por el correo:") {
		t.Errorf("correo: %v", err)
	}
	if e.status() != domain.CampaignDocsDone || len(e.deliv.m) != 0 || len(e.doc.queue.tasks) != 0 {
		t.Errorf("no debe haber cambiado nada: %s %d %d", e.status(), len(e.deliv.m), len(e.doc.queue.tasks))
	}
}

func TestSinFilasConDocumentoNoSeDespacha(t *testing.T) {
	e := newDispEnv(t, nil)
	for id, r := range e.doc.camp.repo.recs[e.doc.cid] {
		r.Documento = nil
		e.doc.camp.repo.recs[e.doc.cid][id] = r
	}
	var ie *ports.InputError
	if _, err := e.svc.Dispatch(context.Background(), e.doc.cid, ports.DispatchInput{}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "ninguna fila válida tiene su PDF") {
		t.Errorf("%v", err)
	}
	// Una fila sin PDF (fallo al generar) no recibe entregas, pero las demás sí.
	e = newDispEnv(t, nil)
	r := e.doc.recs[0]
	cur := e.doc.camp.repo.recs[e.doc.cid][r.ID]
	cur.Documento = &domain.Document{Error: "falló"}
	e.doc.camp.repo.recs[e.doc.cid][r.ID] = cur
	res := e.dispatch(t)
	if res.NoDocument != 1 || res.Created != 8 {
		t.Errorf("%+v", res)
	}
}

// DISPATCH_ALLOWLIST: en pruebas, a nadie fuera de la lista se le envía nada.
func TestListaDeSeguridadParaPruebas(t *testing.T) {
	e := newDispEnv(t, func(d *DispatchDeps) { d.Settings.Allowlist = []string{"320 932 3449", "Papa@Example.com"} })
	// Solo el móvil del jugador 1 coincide (y su correo es a@b.co: no está en la lista).
	rec := e.doc.camp.repo.recs[e.doc.cid][e.doc.recs[0].ID]
	rec.Movil = "573209323449"
	rec.Correos = []string{"papa@example.com"}
	e.doc.camp.repo.recs[e.doc.cid][rec.ID] = rec
	e.dispatch(t)
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusQueued {
		t.Errorf("el móvil de la lista debe enviarse: %v", d.Status)
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelEmail); d.Status != domain.StatusQueued {
		t.Errorf("el correo de la lista (sin distinguir mayúsculas) debe enviarse: %v", d.Status)
	}
	for _, name := range []string{"Jugador 2", "Jugador 5"} {
		for _, ch := range []domain.Channel{domain.ChannelWhatsApp, domain.ChannelEmail} {
			d := e.delivery(t, name, ch)
			if d.Status != domain.StatusSkipped || !strings.Contains(d.History[len(d.History)-1].Detail, "lista de pruebas") {
				t.Errorf("%s/%s debe omitirse por la lista: %+v", name, ch, d)
			}
		}
	}
	noErrs(t, e.run(t, ports.TaskWASend))
	noErrs(t, e.run(t, ports.TaskEmailSend))
	if len(e.wa.sends) != 1 || e.wa.sends[0].To != "573209323449" || len(e.mail.sent) != 1 || e.mail.sent[0].To[0] != "papa@example.com" {
		t.Errorf("solo debe salir lo autorizado: %+v %+v", e.wa.sends, e.mail.sent)
	}
}

// ---- WhatsApp ----

func TestWhatsAppEnviaConElPDFSubidoComoMedio(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	noErrs(t, e.run(t, ports.TaskWASend))

	if len(e.wa.sends) != 4 || len(e.wa.uploads) != 4 {
		t.Fatalf("envíos=%d subidas=%d", len(e.wa.sends), len(e.wa.uploads))
	}
	first := e.wa.sends[0]
	if first.To != "573209323449" || first.Msg.Name != "purchase_receipt_3" || first.Msg.Language != "es_CO" || !strings.HasPrefix(first.Msg.MediaID, "MEDIA") {
		t.Errorf("mensaje: %+v", first)
	}
	// Parámetros: acudiente y jugador en nombre propio; la beca sin el %.
	if got := strings.Join(first.Msg.BodyParams, "|"); got != "A|30|Jugador 1" {
		t.Errorf("parámetros: %q", got)
	}
	if first.Msg.DocFilename != "Jugador 1 - BECA 30.pdf" || e.wa.uploads[0] != "Jugador 1 - BECA 30.pdf" {
		t.Errorf("nombre visible: %q", first.Msg.DocFilename)
	}
	d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp)
	if d.Status != domain.StatusAccepted || d.ProviderID != "wamid.1" || d.Attempts != 1 || domain.StatusLabel(d.Channel, d.Status) != "Aceptado por WhatsApp" {
		t.Errorf("entrega: %+v", d)
	}
	// El medio queda guardado en el documento para los reintentos.
	for _, r := range e.doc.recipients(t) {
		if r.Documento != nil && r.Documento.Error == "" && r.Estado == domain.RecipientValid && r.Movil != "" && (r.Documento.MediaID == "" || r.Documento.MediaExpiresAt.Before(e.now.Add(28*24*time.Hour))) {
			t.Errorf("%s sin media_id vigente: %+v", r.Jugador, r.Documento)
		}
	}
	// Se registra una nota en Chatwoot (tarea aparte) y la entrega no depende de ella.
	// El texto real encolado identifica al acudiente y la propuesta (nombre, móvil, beca, torneo, PDF, pagos).
	var noteText string
	for _, tk := range e.doc.queue.tasks {
		if tk.Type == ports.TaskNote && strings.Contains(string(tk.Payload), "Jugador 1") {
			var pl struct{ Text string }
			_ = json.Unmarshal(tk.Payload, &pl)
			noteText = pl.Text
		}
	}
	for _, want := range []string{"PROPUESTA ENVIADA por WhatsApp", "• Jugador: Jugador 1", "• Móvil: +573209323449", "• Beca: ", "Torneo: Copa Majestic", "• PDF enviado: ", "• Ver PDF: https://x.test/pdf/"} {
		if !strings.Contains(noteText, want) {
			t.Errorf("la nota encolada no contiene %q:\n%s", want, noteText)
		}
	}
	e.chat.err = errors.New("chatwoot caído")
	if errs := e.run(t, ports.TaskNote); len(errs) != 4 || errs[0] == nil {
		t.Errorf("la nota debe fallar sin afectar la entrega: %v", errs)
	}
	if e.delivery(t, "Jugador 1", domain.ChannelWhatsApp).Status != domain.StatusAccepted {
		t.Error("un fallo de Chatwoot no cambia el estado")
	}
	e.chat.err = nil
	e.doc.queue.tasks = nil
	e.dispatchNotes(t)
	if len(e.chat.notes) != 4 || !strings.Contains(e.chat.notes[0], "573209323449|Jugador 1|📄 Propuesta de beca 30 % enviada por plantilla de WhatsApp (aceptada por Meta, wamid wamid.1)") {
		t.Errorf("notas: %q", e.chat.notes)
	}
	// Las 4 entregas aceptadas y la campaña completa.
	if e.status() != domain.CampaignCompleted {
		t.Errorf("estado final: %s", e.status())
	}
}

// reencola las notas (en el flujo real se encolan al aceptar; aquí se vuelve a ejecutar tras arreglar Chatwoot).
func (e dispEnv) dispatchNotes(t *testing.T) {
	for _, r := range e.doc.recipients(t) {
		if r.Estado == domain.RecipientValid && r.Movil != "" {
			d, _ := e.deliv.Get(context.Background(), e.doc.cid, domain.NewDeliveryKey(e.doc.cid, r.ID, domain.ChannelWhatsApp, domain.KindProposal))
			if d.Status == domain.StatusAccepted {
				e.svc.HandleNote(context.Background(), []byte(fmt.Sprintf(`{"campaign_id":%q,"recipient_id":%q,"text":%q}`, e.doc.cid, r.ID,
					"📄 Propuesta de beca 30 % enviada por plantilla de WhatsApp (aceptada por Meta, wamid "+d.ProviderID+") · x")))
			}
		}
	}
}

func TestWhatsAppReutilizaElMedioYVuelveASubirloSiVencio(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	// El primer envío falla de forma transitoria: el reintento NO vuelve a subir el PDF.
	e.wa.sendErrFn = func(n int) error {
		if n == 1 {
			return &ports.ProviderError{Action: ports.ActionRetry, Code: "130429", Message: "más despacio"}
		}
		return nil
	}
	errs := e.run(t, ports.TaskWASend)
	if errs[0] == nil {
		t.Fatal("el primero debía devolver error para que la cola reintente")
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusRetryWait || d.LastError == nil || d.LastError.Code != "130429" {
		t.Errorf("reintentando: %+v", d)
	}
	uploads := len(e.wa.uploads)
	task := ports.Task{Type: ports.TaskWASend, Payload: payloadFor(t, e, "Jugador 1", domain.ChannelWhatsApp)}
	if err := e.svc.HandleWhatsApp(context.Background(), task.Payload); err != nil {
		t.Fatal(err)
	}
	if len(e.wa.uploads) != uploads {
		t.Errorf("el reintento debe reutilizar el medio (subidas %d → %d)", uploads, len(e.wa.uploads))
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusAccepted || d.Attempts != 2 {
		t.Errorf("tras reintentar: %+v", d)
	}
	// Si el medio venció (más de 29 días), se vuelve a subir.
	e2 := newDispEnv(t, nil)
	e2.dispatch(t, "whatsapp")
	noErrs(t, e2.run(t, ports.TaskWASend))
	rec := e2.doc.camp.repo.recs[e2.doc.cid][e2.doc.recs[0].ID]
	if rec.Documento.MediaValid(e2.now) != true {
		t.Fatal("el medio debía estar vigente")
	}
	if rec.Documento.MediaValid(e2.now.Add(30 * 24 * time.Hour)) {
		t.Error("a los 30 días el medio ya no es válido")
	}
}

func payloadFor(t *testing.T, e *dispEnv, jugador string, ch domain.Channel) []byte {
	for _, r := range e.doc.recipients(t) {
		if r.Jugador == jugador {
			return []byte(fmt.Sprintf(`{"campaign_id":%q,"key":%q}`, e.doc.cid, domain.NewDeliveryKey(e.doc.cid, r.ID, ch, domain.KindProposal)))
		}
	}
	t.Fatal("sin jugador")
	return nil
}

func TestWhatsAppErroresPermanentesYReintentos(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	e.wa.sendErr["573209323449"] = &ports.ProviderError{Action: ports.ActionFail, Code: "131026", Message: "Este número no tiene WhatsApp o no se puede entregar. Verifica el móvil o usa solo correo."}
	noErrs(t, e.run(t, ports.TaskWASend)) // un fallo permanente NO devuelve error a la cola (no se reintenta)
	d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp)
	if d.Status != domain.StatusFailed || d.LastError == nil || !d.LastError.Permanent || d.LastError.Code != "131026" || domain.StatusLabel(d.Channel, d.Status) != "No entregado" {
		t.Errorf("fallida: %+v", d)
	}
	// Los demás siguieron enviándose.
	if e.delivery(t, "Jugador 2", domain.ChannelWhatsApp).Status != domain.StatusAccepted {
		t.Error("un fallo no debe frenar a los demás")
	}
	// Al reenviar, lo permanente NO se reintenta (el dato está mal).
	r, err := e.svc.Retry(context.Background(), e.doc.cid)
	if err != nil || r.Requeued != 0 || r.Permanent != 1 {
		t.Errorf("reenvío: %+v %v", r, err)
	}
}

func TestWhatsAppUltimoIntentoPasaAFallida(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	e.wa.sendErr["573209323449"] = &ports.ProviderError{Action: ports.ActionRetry, Code: "red", Message: "No se pudo conectar con WhatsApp. Se reintentará."}
	ctx := ports.WithAttempt(context.Background(), ports.Attempt{Retried: 5, MaxRetry: 5})
	if err := e.svc.HandleWhatsApp(ctx, payloadFor(t, e, "Jugador 1", domain.ChannelWhatsApp)); err != nil {
		t.Fatal(err)
	}
	d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp)
	if d.Status != domain.StatusFailed || !strings.Contains(d.LastError.Message, "Se agotaron los reintentos") || d.LastError.Permanent {
		t.Errorf("agotada: %+v", d)
	}
	// Como NO es permanente, el operador sí puede reenviarla.
	e.wa.sendErr = map[string]error{}
	r, err := e.svc.Retry(context.Background(), e.doc.cid)
	if err != nil || r.Requeued != 1 {
		t.Fatalf("reenvío: %+v %v", r, err)
	}
	noErrs(t, e.run(t, ports.TaskWASend))
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusAccepted {
		t.Errorf("tras reenviar: %+v", d)
	}
}

// Token vencido: toda la campaña se pausa en lugar de fallar uno por uno; al reanudar continúa.
func TestTokenVencidoPausaLaCampana(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	e.wa.sendErrFn = func(int) error {
		return &ports.ProviderError{Action: ports.ActionPause, Code: "190", Message: "El token de WhatsApp no es válido o venció."}
	}
	tasks := append([]ports.Task(nil), e.doc.queue.tasks...)
	e.doc.queue.tasks = nil
	// La primera tarea descubre el problema y pausa la campaña.
	if err := e.svc.HandleWhatsApp(context.Background(), tasks[0].Payload); err != nil {
		t.Fatal(err)
	}
	if e.status() != domain.CampaignPaused {
		t.Fatalf("estado: %s", e.status())
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusRetryWait {
		t.Errorf("la entrega queda reintentable, no fallida: %+v", d)
	}
	// Las demás tareas ven la pausa y se reprograman SIN enviar ni consumir reintentos.
	sendsBefore := len(e.wa.sends)
	for _, task := range tasks[1:] {
		if err := e.svc.HandleWhatsApp(context.Background(), task.Payload); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.wa.sends) != sendsBefore {
		t.Error("en pausa no se envía nada")
	}
	for _, d := range e.deliv.m {
		if d.Status == domain.StatusFailed {
			t.Errorf("ninguna debe fallar por un problema de toda la campaña: %+v", d)
		}
	}
	resched := 0
	for _, task := range e.doc.queue.tasks {
		if task.ProcessAt.After(e.now) {
			resched++
		}
	}
	if resched != 4 {
		t.Errorf("reprogramadas: %d", resched)
	}
	// El operador corrige el token y reanuda.
	if err := e.svc.Resume(context.Background(), e.doc.cid); err != nil {
		t.Fatal(err)
	}
	e.wa.sendErrFn = nil
	e.now = e.now.Add(2 * time.Minute)
	noErrs(t, e.run(t, ports.TaskWASend))
	if e.status() != domain.CampaignCompleted {
		t.Errorf("tras reanudar: %s", e.status())
	}
	for _, d := range e.deliv.m {
		if d.Channel == domain.ChannelWhatsApp && d.Status != domain.StatusAccepted && d.Status != domain.StatusSkipped {
			t.Errorf("todas deben terminar aceptadas: %+v", d)
		}
	}
	// Reanudar una campaña que no está en pausa es un error claro.
	var ie *ports.InputError
	if err := e.svc.Resume(context.Background(), e.doc.cid); !errors.As(err, &ie) {
		t.Errorf("reanudar sin pausa: %v", err)
	}
}

// El corazón de la confianza: si el proceso muere después de llamar a Meta, NO se reenvía a ciegas.
func TestNoSeReenviaUnEnvioIncierto(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	key := string(payloadFor(t, e, "Jugador 1", domain.ChannelWhatsApp))

	// Simula la caída: la entrega quedó en SENDING y nadie registró el resultado.
	d, _ := e.deliv.Get(context.Background(), e.doc.cid, domain.NewDeliveryKey(e.doc.cid, e.doc.recs[0].ID, domain.ChannelWhatsApp, domain.KindProposal))
	e.deliv.Transition(context.Background(), e.doc.cid, domain.Transition{Key: d.Key, To: domain.StatusSending, Source: domain.SourceWorker, At: e.now})

	// Con el SENDING reciente la tarea no envía (otro proceso podría estar trabajando): error para reintentar luego.
	if err := e.svc.HandleWhatsApp(context.Background(), []byte(key)); err == nil {
		t.Error("un SENDING reciente debe devolver error, no enviar")
	}
	if len(e.wa.sends) != 0 {
		t.Fatal("no debe enviarse nada")
	}
	// Pasados más de 5 minutos pasa a revisión y TAMPOCO se envía.
	e.now = e.now.Add(6 * time.Minute)
	if err := e.svc.HandleWhatsApp(context.Background(), []byte(key)); err != nil {
		t.Fatal(err)
	}
	d, _ = e.deliv.Get(context.Background(), e.doc.cid, d.Key)
	if d.Status != domain.StatusNeedsReview || d.LastError == nil || len(e.wa.sends) != 0 {
		t.Fatalf("debe quedar en revisión sin enviar: %+v sends=%d", d, len(e.wa.sends))
	}
	if domain.StatusLabel(d.Channel, d.Status) != "Revisar" {
		t.Error("etiqueta")
	}
	// Solo el operador puede decidir reenviarla.
	noErrs(t, e.run(t, ports.TaskWASend)) // el resto de las tareas siguen normales
	if d, _ := e.deliv.Get(context.Background(), e.doc.cid, d.Key); d.Status != domain.StatusNeedsReview {
		t.Errorf("sigue en revisión: %v", d.Status)
	}
	r, err := e.svc.Retry(context.Background(), e.doc.cid)
	if err != nil || r.Requeued != 1 {
		t.Fatalf("reenvío del operador: %+v %v", r, err)
	}
	noErrs(t, e.run(t, ports.TaskWASend))
	if d, _ := e.deliv.Get(context.Background(), e.doc.cid, d.Key); d.Status != domain.StatusAccepted {
		t.Errorf("tras la decisión del operador: %v", d.Status)
	}
}

func TestTareasRepetidasNoDuplicanElEnvio(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	key := payloadFor(t, e, "Jugador 1", domain.ChannelWhatsApp)
	for i := 0; i < 3; i++ { // asynq entrega la misma tarea varias veces (reinicio, reintento…)
		if err := e.svc.HandleWhatsApp(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.wa.sends); n != 1 {
		t.Errorf("enviado %d veces", n)
	}
	// Cargas inválidas o de otra campaña no se reintentan.
	for _, bad := range []string{`{`, `{"campaign_id":"x","key":"y"}`, `{"campaign_id":"otra","key":"a:b:whatsapp:proposal"}`, fmt.Sprintf(`{"campaign_id":%q,"key":%q}`, e.doc.cid, e.doc.cid+":rx:email:proposal")} {
		if err := e.svc.HandleWhatsApp(context.Background(), []byte(bad)); !errors.Is(err, ports.ErrSkipRetry) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// Entrega o campaña inexistentes: permanente.
	missing := fmt.Sprintf(`{"campaign_id":%q,"key":%q}`, e.doc.cid, e.doc.cid+":nope:whatsapp:proposal")
	if err := e.svc.HandleWhatsApp(context.Background(), []byte(missing)); !errors.Is(err, ports.ErrSkipRetry) {
		t.Errorf("entrega inexistente: %v", err)
	}
	if err := e.svc.HandleWhatsApp(context.Background(), []byte(`{"campaign_id":"nope","key":"nope:r:whatsapp:proposal"}`)); !errors.Is(err, ports.ErrSkipRetry) {
		t.Errorf("campaña inexistente: %v", err)
	}
}

func TestRespuestaIncierta200SinWamidQuedaEnRevision(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	e.wa.sendErr["573209323449"] = &ports.ProviderError{Action: ports.ActionReview, Code: "sin-wamid", Message: "WhatsApp respondió sin identificador de mensaje."}
	noErrs(t, e.run(t, ports.TaskWASend))
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusNeedsReview || len(e.wa.sends) != 4 {
		t.Errorf("revisión: %+v", d)
	}
}

func TestMedioRechazadoSeVuelveASubir(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	n := 0
	e.wa.sendErrFn = func(int) error {
		n++
		if n == 1 {
			return &ports.ProviderError{Action: ports.ActionRetry, Code: "131053", Message: "WhatsApp no pudo procesar el PDF."}
		}
		return nil
	}
	e.run(t, ports.TaskWASend)
	subidas := len(e.wa.uploads)
	if err := e.svc.HandleWhatsApp(context.Background(), payloadFor(t, e, "Jugador 1", domain.ChannelWhatsApp)); err != nil {
		t.Fatal(err)
	}
	if len(e.wa.uploads) != subidas+1 {
		t.Errorf("tras 131053 debe subir el PDF de nuevo: %d → %d", subidas, len(e.wa.uploads))
	}
}

func TestSinDocumentoLaEntregaFalla(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "whatsapp")
	r := e.doc.camp.repo.recs[e.doc.cid][e.doc.recs[0].ID]
	r.Documento = nil
	e.doc.camp.repo.recs[e.doc.cid][r.ID] = r
	noErrs(t, e.run(t, ports.TaskWASend))
	if d := e.delivery(t, "Jugador 1", domain.ChannelWhatsApp); d.Status != domain.StatusFailed || !strings.Contains(d.LastError.Message, "PDF") {
		t.Errorf("%+v", d)
	}
	if len(e.wa.sends) != 3 {
		t.Errorf("las demás siguen: %d", len(e.wa.sends))
	}
}

// ---- Correo ----

func TestCorreoEnviaConElAdjuntoYLaCabecera(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t, "email")
	noErrs(t, e.run(t, ports.TaskEmailSend))
	if len(e.mail.sent) != 4 || e.quota.used != 4 {
		t.Fatalf("enviados=%d cuota=%d", len(e.mail.sent), e.quota.used)
	}
	m := e.mail.sent[0]
	if m.To[0] != "a@b.co" || m.Attachments[0].Filename != "Jugador 1 - BECA 30.pdf" || string(m.Attachments[0].Data) != "%PDF-1.4 fake" || !strings.HasPrefix(m.Headers["X-Visoria-Delivery"], e.doc.cid+":") {
		t.Errorf("correo: %+v", m)
	}
	d := e.delivery(t, "Jugador 1", domain.ChannelEmail)
	if d.Status != domain.StatusAccepted || d.ProviderID != "<m1@x>" || domain.StatusLabel(d.Channel, d.Status) != "Aceptado por Gmail" {
		t.Errorf("entrega: %+v", d)
	}
	// Un correo nunca puede llegar a «Entregado»: el protocolo no lo confirma.
	tr, _ := e.deliv.Transition(context.Background(), e.doc.cid, domain.Transition{Key: d.Key, To: domain.StatusDelivered, Source: domain.SourceWebhook, At: e.now})
	if tr.Outcome != domain.OutcomeRejected {
		t.Errorf("un correo no puede estar entregado: %v", tr.Outcome)
	}
	if e.status() != domain.CampaignCompleted {
		t.Errorf("estado: %s", e.status())
	}
}

func TestCorreoCuotaDiariaReprogramaParaManana(t *testing.T) {
	e := newDispEnv(t, nil)
	e.quota.limit = 2
	e.dispatch(t, "email")
	noErrs(t, e.run(t, ports.TaskEmailSend))
	if len(e.mail.sent) != 2 {
		t.Fatalf("solo 2 caben hoy: %d", len(e.mail.sent))
	}
	// Las otras 2 quedan EN COLA (no fallidas) y reprogramadas para mañana a las 08:00 de Bogotá.
	var next time.Time
	for _, task := range e.doc.queue.tasks {
		if task.Type == ports.TaskEmailSend && task.ProcessAt.After(e.now) {
			next = task.ProcessAt
		}
	}
	if want := time.Date(2026, 11, 6, 8, 0, 0, 0, bogota); !next.Equal(want) {
		t.Errorf("reprogramado para %v, quería %v", next, want)
	}
	pend := 0
	for _, d := range e.deliv.m {
		if d.Channel == domain.ChannelEmail && d.Status == domain.StatusQueued {
			pend++
		}
	}
	if pend != 2 || e.status() != domain.CampaignDispatching {
		t.Errorf("pendientes=%d estado=%s", pend, e.status())
	}
	var notice bool
	for _, ev := range e.doc.events.events {
		if ev.Type == "notice" {
			notice = true
		}
	}
	if !notice {
		t.Error("debe avisar al operador")
	}
	// Al día siguiente (cuota nueva) salen los 2 restantes.
	e.now = next.Add(time.Minute)
	e.quota.used = 0
	noErrs(t, e.run(t, ports.TaskEmailSend))
	if len(e.mail.sent) != 4 || e.status() != domain.CampaignCompleted {
		t.Errorf("al día siguiente: enviados=%d estado=%s", len(e.mail.sent), e.status())
	}
}

func TestCorreoErroresDelServidor(t *testing.T) {
	// Dirección inexistente: fallo permanente de esa fila.
	e := newDispEnv(t, nil)
	e.dispatch(t, "email")
	e.mail.sendErr = &ports.ProviderError{Action: ports.ActionFail, Code: "5.1.1", Message: "La dirección de correo no existe o fue rechazada."}
	noErrs(t, e.run(t, ports.TaskEmailSend))
	for _, d := range e.deliv.m {
		if d.Channel == domain.ChannelEmail && d.Status != domain.StatusSkipped && (d.Status != domain.StatusFailed || !d.LastError.Permanent) {
			t.Errorf("%+v", d)
		}
	}
	// Credenciales malas: pausa.
	e = newDispEnv(t, nil)
	e.dispatch(t, "email")
	e.mail.sendErr = &ports.ProviderError{Action: ports.ActionPause, Code: "5.7.8", Message: "no aceptó la contraseña"}
	tasks := e.doc.queue.tasks
	e.doc.queue.tasks = nil
	if err := e.svc.HandleEmail(context.Background(), tasks[0].Payload); err != nil || e.status() != domain.CampaignPaused {
		t.Errorf("credenciales: %v %s", err, e.status())
	}
	// Cuota que Gmail anuncia a mitad de camino (Defer): se reprograma sin consumir reintentos.
	e = newDispEnv(t, nil)
	e.dispatch(t, "email")
	e.mail.sendErr = &ports.ProviderError{Action: ports.ActionDefer, Code: "5.4.5", Message: "límite diario"}
	tasks = e.doc.queue.tasks
	e.doc.queue.tasks = nil
	if err := e.svc.HandleEmail(context.Background(), tasks[0].Payload); err != nil {
		t.Fatal(err)
	}
	if d := e.delivery(t, "Jugador 1", domain.ChannelEmail); d.Status != domain.StatusRetryWait || len(e.doc.queue.tasks) != 1 || !e.doc.queue.tasks[0].ProcessAt.After(e.now) {
		t.Errorf("defer: %+v tareas=%d", d, len(e.doc.queue.tasks))
	}
	// Error de la cuota (Redis): se reintenta, no se envía.
	e = newDispEnv(t, nil)
	e.dispatch(t, "email")
	e.quota.err = errors.New("redis")
	if errs := e.run(t, ports.TaskEmailSend); errs[0] == nil || len(e.mail.sent) != 0 {
		t.Errorf("sin cuota no se envía: %v", errs)
	}
}

// ---- Reenvío, vista y completitud ----

func TestSnapshotMuestraElEstadoHonesto(t *testing.T) {
	e := newDispEnv(t, nil)
	e.dispatch(t)
	e.wa.sendErr["573209323449"] = &ports.ProviderError{Action: ports.ActionFail, Code: "131026", Message: "Este número no tiene WhatsApp."}
	noErrs(t, e.run(t, ports.TaskWASend))
	noErrs(t, e.run(t, ports.TaskEmailSend))
	snap, err := e.svc.Snapshot(context.Background(), e.doc.cid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != string(domain.CampaignCompleted) || snap.InFlight != 0 {
		t.Errorf("estado: %s en_curso=%d", snap.Status, snap.InFlight)
	}
	wa, em := snap.Progress["whatsapp"], snap.Progress["email"]
	if wa.Total != 5 || wa.Counts["ACCEPTED"] != 3 || wa.Counts["FAILED"] != 1 || wa.Counts["SKIPPED"] != 1 || em.Total != 5 || em.Counts["ACCEPTED"] != 4 || em.Counts["SKIPPED"] != 1 {
		t.Errorf("progreso: %+v %+v", wa, em)
	}
	if len(snap.Deliveries) != 10 || snap.Deliveries[0].Fila > snap.Deliveries[9].Fila {
		t.Fatalf("entregas: %d", len(snap.Deliveries))
	}
	byKey := map[string]ports.DeliveryView{}
	for _, d := range snap.Deliveries {
		byKey[fmt.Sprintf("%s/%s", d.Jugador, d.Channel)] = d
	}
	if d := byKey["Jugador 1/whatsapp"]; d.Label != "No entregado" || d.Error != "Este número no tiene WhatsApp." || !d.Permanent {
		t.Errorf("fallida: %+v", d)
	}
	if d := byKey["Jugador 2/email"]; d.Label != "Aceptado por Gmail" {
		t.Errorf("correo: %+v", d)
	}
	if d := byKey["Jugador 3/whatsapp"]; d.Label != "Omitido" || d.Error != "Sin móvil válido" {
		t.Errorf("omitida: %+v", d)
	}
	if _, err := e.svc.Snapshot(context.Background(), "nope"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Errorf("inexistente: %v", err)
	}
}

func TestReenvioRequiereDespachoPrevio(t *testing.T) {
	e := newDispEnv(t, nil)
	var ie *ports.InputError
	if _, err := e.svc.Retry(context.Background(), e.doc.cid); !errors.As(err, &ie) {
		t.Errorf("antes de despachar: %v", err)
	}
	e.dispatch(t, "whatsapp")
	cc := e.doc.camp.repo.camps[e.doc.cid]
	cc.Status = domain.CampaignPaused
	e.doc.camp.repo.camps[e.doc.cid] = cc
	if _, err := e.svc.Retry(context.Background(), e.doc.cid); !errors.Is(err, ports.ErrCampaignPaused) {
		t.Errorf("en pausa: %v", err)
	}
	cc.Status = domain.CampaignDispatching
	e.doc.camp.repo.camps[e.doc.cid] = cc
	if r, err := e.svc.Retry(context.Background(), e.doc.cid); err != nil || r.Requeued != 0 {
		t.Errorf("sin nada que reenviar: %+v %v", r, err)
	}
}

func TestTodoOmitidoCompletaLaCampana(t *testing.T) {
	e := newDispEnv(t, func(d *DispatchDeps) { d.Settings.Allowlist = []string{"573000000000"} })
	res := e.dispatch(t, "whatsapp")
	if res.Queued != 0 || res.Skipped != 5 {
		t.Errorf("%+v", res)
	}
	if e.status() != domain.CampaignCompleted {
		t.Errorf("si todo se omite, la campaña termina: %s", e.status())
	}
}

func TestNormalizeTargetYHelpers(t *testing.T) {
	for in, want := range map[string]string{" Papa@Example.COM ": "papa@example.com", "320 932 3449": "573209323449", "573209323449": "573209323449", "abc": "", "": ""} {
		if got := normalizeTarget(in); got != want {
			t.Errorf("normalizeTarget(%q)=%q", in, got)
		}
	}
	if got := nextMorning(time.Date(2026, 11, 5, 23, 30, 0, 0, bogota)); !got.Equal(time.Date(2026, 11, 6, 8, 0, 0, 0, bogota)) {
		t.Errorf("nextMorning: %v", got)
	}
	// 04:00 UTC del día 6 sigue siendo el día 5 en Bogotá (23:00): mañana = día 6.
	if got := nextMorning(time.Date(2026, 11, 6, 4, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 6, 8, 0, 0, 0, bogota)) {
		t.Errorf("nextMorning cruzando UTC: %v", got)
	}
}
