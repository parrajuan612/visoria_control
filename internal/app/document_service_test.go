package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// ---- simulados ----

type fakeRender struct {
	mu    sync.Mutex
	calls []ports.DocumentInput
	err   map[string]error // por nombre de jugador
}

func (f *fakeRender) Render(_ context.Context, tpl []byte, in ports.DocumentInput) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	f.mu.Unlock()
	if e := f.err[in.Recipient.Jugador]; e != nil {
		return nil, e
	}
	return []byte("DOCX:" + string(tpl) + ":" + in.Recipient.Jugador), nil
}
func (f *fakeRender) ValidateTemplate(data []byte) string {
	if strings.HasPrefix(string(data), "MALA") {
		return "No encontré el campo «Club: XXXX»."
	}
	return ""
}

type fakeConv struct {
	calls   [][]string
	failing map[string]bool // nombre base que no genera PDF
	err     error
}

func (f *fakeConv) Convert(_ context.Context, files []string, out string) error {
	f.calls = append(f.calls, append([]string(nil), files...))
	if f.err != nil {
		return f.err
	}
	os.MkdirAll(out, 0o750)
	var failed []string
	for _, p := range files {
		base := strings.TrimSuffix(filepath.Base(p), ".docx")
		if f.failing[base] {
			failed = append(failed, base)
			continue
		}
		os.WriteFile(filepath.Join(out, base+".pdf"), []byte("%PDF-1.4 fake"), 0o600)
	}
	if len(failed) > 0 {
		return &ports.ConvertError{Failed: failed}
	}
	return nil
}

type fakeTpls struct {
	have map[string]string
	put  map[string][]byte
	err  error
}

func (f *fakeTpls) Get(_ context.Context, k string) ([]byte, error) {
	if v, ok := f.have[k]; ok {
		return []byte(v), nil
	}
	return nil, ports.ErrTemplateNotFound
}
func (f *fakeTpls) Put(_ context.Context, k string, d []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.put == nil {
		f.put = map[string][]byte{}
	}
	_, had := f.put[k]
	f.put[k] = d
	if had {
		return k + "_20261105_150000.docx", nil
	}
	return "", nil
}
func (f *fakeTpls) List(context.Context) ([]ports.TemplateInfo, error) { return nil, nil }

type fakeQueue struct {
	mu    sync.Mutex
	tasks []ports.Task
	seen  map[string]bool
	err   error
}

func (q *fakeQueue) Enqueue(_ context.Context, t ports.Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	if q.seen == nil {
		q.seen = map[string]bool{}
	}
	if q.seen[t.TaskID] {
		return ports.ErrDuplicateTask
	}
	q.seen[t.TaskID] = true
	q.tasks = append(q.tasks, t)
	return nil
}

type fakeEvents struct {
	mu     sync.Mutex
	events []struct {
		Type string
		Data any
	}
	err error
}

func (e *fakeEvents) Publish(_ context.Context, _ string, typ string, data any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.events = append(e.events, struct {
		Type string
		Data any
	}{typ, data})
	return nil
}

type tmpWork struct{ base string }

func (w tmpWork) Open() (string, func(), error) {
	d, err := os.MkdirTemp(w.base, "w-")
	return d, func() { os.RemoveAll(d) }, err
}
func (tmpWork) WriteFile(p string, d []byte) error { return os.WriteFile(p, d, 0o600) }

// ---- entorno ----

type docEnv struct {
	camp   campEnv
	svc    *DocumentService
	rend   *fakeRender
	conv   *fakeConv
	tpls   *fakeTpls
	queue  *fakeQueue
	events *fakeEvents
	work   string
	cid    string
	recs   []domain.Recipient
}

// 5 filas válidas (cadete_juvenil), 1 con error y 1 sin beca.
func newDocEnv(t *testing.T, batch int) docEnv {
	t.Helper()
	var rs []domain.RawRow
	for i := 0; i < 5; i++ {
		rs = append(rs, domain.RawRow{Fila: i + 2, Jugador: fmt.Sprintf("Jugador %d", i+1), Club: "C", Beca: "30%", Anio: "2012", Acudiente: "A", Movil: "3209323449", Correo: "a@b.co"})
	}
	rs = append(rs, domain.RawRow{Fila: 7, Jugador: "Con Error", Club: "C", Beca: "30%", Anio: "2020", Acudiente: "A", Movil: "3209323449"},
		domain.RawRow{Fila: 8, Jugador: "Sin Beca", Club: "C", Beca: "SIN BECA"})
	e := newCampSvc(t, rs, campCfg())
	d, err := create(t, e, func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"2026-12-01", "2027-01-01", "2027-02-01"} })
	if err != nil {
		t.Fatal(err)
	}
	de := docEnv{camp: e, rend: &fakeRender{err: map[string]error{}}, conv: &fakeConv{failing: map[string]bool{}},
		tpls: &fakeTpls{have: map[string]string{"cadete_juvenil": "TPL"}}, queue: &fakeQueue{}, events: &fakeEvents{},
		work: t.TempDir(), cid: d.Campaign.ID, recs: d.Recipients}
	de.svc = NewDocumentService(DocumentDeps{Repo: e.repo, Files: e.files, Templates: de.tpls, Render: de.rend, Converter: de.conv,
		Queue: de.queue, Events: de.events, Locks: e.svc, Workspace: tmpWork{de.work}, BatchSize: batch})
	de.svc.now = func() time.Time { return time.Date(2026, 11, 5, 21, 30, 0, 0, time.UTC) } // 16:30 en Bogotá
	return de
}

func (d docEnv) recipients(t *testing.T) []domain.Recipient {
	r, err := d.camp.repo.Recipients(context.Background(), d.cid)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (d docEnv) status() domain.CampaignStatus { return d.camp.repo.camps[d.cid].Status }

func (d docEnv) runAll(t *testing.T) {
	t.Helper()
	for _, task := range d.queue.tasks {
		if err := d.svc.HandleRender(context.Background(), task.Payload); err != nil {
			t.Fatalf("HandleRender: %v", err)
		}
	}
}

// ---- Generate ----

func TestGenerateEncolaLotes(t *testing.T) {
	d := newDocEnv(t, 2)
	res, err := d.svc.Generate(context.Background(), d.cid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 5 || res.Queued != 5 || res.Batches != 3 || len(d.queue.tasks) != 3 {
		t.Fatalf("resultado: %+v tareas=%d", res, len(d.queue.tasks))
	}
	var sizes []int
	for _, task := range d.queue.tasks {
		var p DocPayload
		json.Unmarshal(task.Payload, &p)
		sizes = append(sizes, len(p.RecipientIDs))
		if task.Type != ports.TaskDocRender || task.Queue != ports.QueueDocs || p.CampaignID != d.cid || !strings.HasPrefix(task.TaskID, d.cid+":docs:") || task.MaxRetry != 3 {
			t.Errorf("tarea: %+v", task)
		}
	}
	if fmt.Sprint(sizes) != "[2 2 1]" {
		t.Errorf("lotes: %v", sizes)
	}
	if d.status() != domain.CampaignDocsRunning {
		t.Errorf("estado: %s", d.status())
	}
	// Mientras se genera: no se puede pedir otra vez ni editar.
	if _, err := d.svc.Generate(context.Background(), d.cid); !errors.Is(err, ports.ErrGenerationRunning) {
		t.Errorf("segunda petición: %v", err)
	}
	s := func(v string) *string { return &v }
	if _, err := d.camp.svc.EditRecipient(context.Background(), d.cid, d.recs[0].ID, domain.RecipientPatch{Club: s("x")}, "op"); !errors.Is(err, ports.ErrCampaignLocked) {
		t.Errorf("editar durante la generación: %v", err)
	}
}

func TestGenerateErrores(t *testing.T) {
	ctx := context.Background()
	var ie *ports.InputError
	// Falta la plantilla: mensaje del cliente y la campaña NO cambia de estado.
	d := newDocEnv(t, 25)
	d.tpls.have = map[string]string{}
	if _, err := d.svc.Generate(ctx, d.cid); !errors.As(err, &ie) || ie.Message != "Falta la plantilla «cadete_juvenil.docx» en la carpeta plantillas." {
		t.Errorf("sin plantilla: %v", err)
	}
	if d.status() != domain.CampaignDraft || len(d.queue.tasks) != 0 {
		t.Errorf("no debe cambiar nada: %s %d", d.status(), len(d.queue.tasks))
	}
	// Campaña inexistente / bloqueada.
	if _, err := d.svc.Generate(ctx, "nope"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Errorf("inexistente: %v", err)
	}
	d.camp.repo.camps[d.cid] = func() domain.Campaign { c := d.camp.repo.camps[d.cid]; c.Status = domain.CampaignDispatching; return c }()
	if _, err := d.svc.Generate(ctx, d.cid); !errors.Is(err, ports.ErrCampaignLocked) {
		t.Errorf("bloqueada: %v", err)
	}
	// Si no se puede encolar, la campaña vuelve a su estado anterior.
	d = newDocEnv(t, 25)
	d.queue.err = errors.New("redis caído")
	if _, err := d.svc.Generate(ctx, d.cid); err == nil || d.status() != domain.CampaignDraft {
		t.Errorf("encolar falla: %v estado=%s", err, d.status())
	}
	// Nada pendiente → no encola ni cambia el estado.
	d = newDocEnv(t, 25)
	d.svc.Generate(ctx, d.cid)
	d.runAll(t)
	res, err := d.svc.Generate(ctx, d.cid)
	if err != nil || res.Queued != 0 || res.Pending != 0 || len(d.queue.tasks) != 1 {
		t.Errorf("sin pendientes: %+v %v", res, err)
	}
}

// ---- HandleRender ----

func TestHandleRenderGeneraGuardaPublicaYTermina(t *testing.T) {
	d := newDocEnv(t, 25)
	if _, err := d.svc.Generate(context.Background(), d.cid); err != nil {
		t.Fatal(err)
	}
	d.runAll(t)

	valid := 0
	for _, r := range d.recipients(t) {
		switch r.Estado {
		case domain.RecipientValid:
			valid++
			if !r.CanDispatch() || r.Documento.SHA256 != "sha-"+r.ID || r.Documento.Path != "pdf/"+r.ID+".pdf" || r.Documento.GeneratedAt.IsZero() {
				t.Errorf("%s debía quedar con documento: %+v", r.Jugador, r.Documento)
			}
			if string(d.camp.files.pdfs[r.ID]) != "%PDF-1.4 fake" {
				t.Errorf("PDF de %s no guardado", r.Jugador)
			}
		default:
			if r.Documento != nil {
				t.Errorf("%s (%s) no debe tener documento", r.Jugador, r.Estado)
			}
		}
	}
	if valid != 5 || len(d.conv.calls) != 1 || len(d.conv.calls[0]) != 5 {
		t.Errorf("válidas=%d conversiones=%v (una sola llamada con las 5)", valid, d.conv.calls)
	}
	if d.status() != domain.CampaignDocsDone {
		t.Errorf("estado final: %s", d.status())
	}
	// Datos que llegan al renderizador: torneo, fecha de emisión en hora de Bogotá y las N primeras fechas del plan.
	in := d.rend.calls[0]
	if in.Torneo != "Copa Majestic" || in.FechaEmision != "05/11/2026" || strings.Join(in.FechasPago, ",") != "01/12/2026" { // el plan del 30 % en esta configuración tiene 1 pago
		t.Errorf("entrada: %+v", in)
	}
	// El área de trabajo se limpia.
	if left, _ := os.ReadDir(d.work); len(left) != 0 {
		t.Errorf("área de trabajo sin limpiar: %v", left)
	}
	// Eventos: uno "document" por fila (con progreso) y uno "campaign" al terminar.
	var docs, camps int
	for _, e := range d.events.events {
		switch e.Type {
		case "document":
			docs++
			if ev := e.Data.(DocumentEvent); !ev.OK || ev.Total != 5 || ev.Done != 5 {
				t.Errorf("evento: %+v", ev)
			}
		case "campaign":
			camps++
		}
	}
	if docs != 5 || camps != 1 {
		t.Errorf("eventos: documentos=%d campaña=%d", docs, camps)
	}
}

func TestHandleRenderErroresPorFilaNoDetienenElLote(t *testing.T) {
	d := newDocEnv(t, 25)
	d.rend.err["Jugador 2"] = &ports.DocError{Message: "Quedaron campos «XXX» sin reemplazar en el documento."}
	d.rend.err["Jugador 3"] = errors.New("boom")
	d.conv.failing[d.recs[3].ID] = true // Jugador 4: LibreOffice no generó su PDF
	d.camp.files.failPDF = map[string]error{d.recs[4].ID: errors.New("disco lleno")}
	d.svc.Generate(context.Background(), d.cid)
	d.runAll(t)

	got := map[string]*domain.Document{}
	for _, r := range d.recipients(t) {
		got[r.Jugador] = r.Documento
	}
	if got["Jugador 1"] == nil || got["Jugador 1"].Error != "" {
		t.Errorf("Jugador 1: %+v", got["Jugador 1"])
	}
	for name, sub := range map[string]string{
		"Jugador 2": "Quedaron campos «XXX»", "Jugador 3": "Error inesperado al crear el documento: boom",
		"Jugador 4": "No se pudo convertir el documento a PDF.", "Jugador 5": "No se pudo guardar el PDF: disco lleno",
	} {
		if got[name] == nil || !strings.Contains(got[name].Error, sub) || got[name].Path != "" {
			t.Errorf("%s: %+v (quería %q)", name, got[name], sub)
		}
	}
	// Ninguna de las fallidas puede recibir entregas, y la campaña igual termina (no hay pendientes).
	for _, r := range d.recipients(t) {
		if r.Jugador != "Jugador 1" && r.CanDispatch() {
			t.Errorf("%s no debe poder despacharse", r.Jugador)
		}
	}
	if d.status() != domain.CampaignDocsDone {
		t.Errorf("estado: %s", d.status())
	}
	// Solo se convirtieron las que sí se generaron (sin las 2 con error de render).
	if len(d.conv.calls[0]) != 3 {
		t.Errorf("a convertir: %d", len(d.conv.calls[0]))
	}
	var failedEv int
	for _, e := range d.events.events {
		if ev, ok := e.Data.(DocumentEvent); ok && !ev.OK && ev.Error != "" {
			failedEv++
		}
	}
	if failedEv != 4 {
		t.Errorf("eventos de error: %d", failedEv)
	}
}

func TestHandleRenderConvertidorNoDisponibleOTransitorio(t *testing.T) {
	// Sin LibreOffice: error permanente → todas con el motivo, no se reintenta.
	d := newDocEnv(t, 25)
	d.conv.err = ports.ErrConverterUnavailable
	d.svc.Generate(context.Background(), d.cid)
	d.runAll(t)
	for _, r := range d.recipients(t) {
		if r.Estado == domain.RecipientValid && (r.Documento == nil || !strings.Contains(r.Documento.Error, "LibreOffice no está instalado")) {
			t.Errorf("%s: %+v", r.Jugador, r.Documento)
		}
	}
	// Plazo vencido: error transitorio → la tarea falla para que asynq la reintente, y NO se guarda nada.
	d = newDocEnv(t, 25)
	d.conv.err = errors.New("LibreOffice tardó más de 10m0s y se canceló")
	d.svc.Generate(context.Background(), d.cid)
	if err := d.svc.HandleRender(context.Background(), d.queue.tasks[0].Payload); err == nil || errors.Is(err, ports.ErrSkipRetry) {
		t.Fatalf("debía devolver un error reintentable: %v", err)
	}
	for _, r := range d.recipients(t) {
		if r.Documento != nil {
			t.Errorf("no debe quedar nada guardado: %s", r.Jugador)
		}
	}
	if d.status() != domain.CampaignDocsRunning {
		t.Errorf("sigue en curso: %s", d.status())
	}
	// Al reintentar (ya funciona) termina bien.
	d.conv.err = nil
	d.runAll(t)
	if d.status() != domain.CampaignDocsDone {
		t.Errorf("tras reintentar: %s", d.status())
	}
}

func TestHandleRenderEsIdempotenteYDescartaLoObsoleto(t *testing.T) {
	d := newDocEnv(t, 25)
	d.svc.Generate(context.Background(), d.cid)
	d.runAll(t)
	n := len(d.rend.calls)
	// Reejecutar la misma tarea (reintento de asynq) no vuelve a generar nada.
	if err := d.svc.HandleRender(context.Background(), d.queue.tasks[0].Payload); err != nil || len(d.rend.calls) != n {
		t.Errorf("idempotencia: %v (renders %d→%d)", err, n, len(d.rend.calls))
	}
	// Si la fila cambió mientras se generaba, el documento se descarta (no corresponde a sus datos).
	d = newDocEnv(t, 25)
	d.svc.Generate(context.Background(), d.cid)
	rid := d.recs[0].ID
	d.rend.err = map[string]error{}
	orig := d.svc.render
	d.svc.render = renderFunc(func(ctx context.Context, tpl []byte, in ports.DocumentInput) ([]byte, error) {
		if in.Recipient.ID == rid { // simula una edición que llega a mitad de la generación
			r := d.camp.repo.recs[d.cid][rid]
			r.Raw.Club = "Otro club"
			d.camp.repo.recs[d.cid][rid] = r
		}
		return orig.Render(ctx, tpl, in)
	})
	d.runAll(t)
	for _, r := range d.recipients(t) {
		if r.ID == rid && r.Documento != nil {
			t.Errorf("el documento obsoleto no debía guardarse: %+v", r.Documento)
		}
		if r.ID != rid && r.Estado == domain.RecipientValid && !r.HasDocument() {
			t.Errorf("%s debía quedar con documento", r.Jugador)
		}
	}
	// Quedó pendiente: la campaña NO está terminada y se puede volver a pedir.
	if d.status() != domain.CampaignDocsRunning {
		t.Errorf("estado: %s", d.status())
	}
}

type renderFunc func(context.Context, []byte, ports.DocumentInput) ([]byte, error)

func (f renderFunc) Render(c context.Context, t []byte, in ports.DocumentInput) ([]byte, error) {
	return f(c, t, in)
}
func (renderFunc) ValidateTemplate([]byte) string { return "" }

func TestHandleRenderCargasInvalidas(t *testing.T) {
	d := newDocEnv(t, 25)
	for name, p := range map[string]string{"json roto": "{", "sin campaña": `{"recipient_ids":["x"]}`, "campaña inexistente": `{"campaign_id":"nope","recipient_ids":["x"]}`} {
		if err := d.svc.HandleRender(context.Background(), []byte(p)); !errors.Is(err, ports.ErrSkipRetry) {
			t.Errorf("%s: debe ser permanente: %v", name, err)
		}
	}
	// Los eventos que fallan no rompen la generación.
	d = newDocEnv(t, 25)
	d.events.err = errors.New("redis")
	d.svc.Generate(context.Background(), d.cid)
	d.runAll(t)
	if d.status() != domain.CampaignDocsDone {
		t.Errorf("un fallo de eventos no debe impedir terminar: %s", d.status())
	}
}

// Editar una fila invalida SOLO su documento (SDD §4.3) y permite regenerarlo.
func TestEditarInvalidaElDocumentoDeEsaFila(t *testing.T) {
	d := newDocEnv(t, 25)
	ctx := context.Background()
	d.svc.Generate(ctx, d.cid)
	d.runAll(t)
	if d.status() != domain.CampaignDocsDone {
		t.Fatal("debía estar lista")
	}
	s := func(v string) *string { return &v }
	res, err := d.camp.svc.EditRecipient(ctx, d.cid, d.recs[1].ID, domain.RecipientPatch{Acudiente: s("Otra Persona")}, "op")
	if err != nil {
		t.Fatal(err)
	}
	if res.Recipient.Documento != nil || res.Recipient.CanDispatch() {
		t.Errorf("la fila editada debe quedar sin documento: %+v", res.Recipient.Documento)
	}
	if last := d.camp.files.events[len(d.camp.files.events)-1]; !strings.Contains(last.Detail, "el documento generado quedó obsoleto") {
		t.Errorf("la auditoría debe decirlo: %q", last.Detail)
	}
	rs := d.recipients(t)
	for _, r := range rs {
		if r.ID != d.recs[1].ID && r.Estado == domain.RecipientValid && !r.HasDocument() {
			t.Errorf("%s no debía perder su documento", r.Jugador)
		}
	}
	if p := domain.PendingDocuments(rs); len(p) != 1 || p[0].ID != d.recs[1].ID {
		t.Fatalf("pendientes: %+v", p)
	}
	// Se regenera solo esa fila.
	before := len(d.rend.calls)
	res2, err := d.svc.Generate(ctx, d.cid)
	if err != nil || res2.Queued != 1 || res2.Pending != 1 {
		t.Fatalf("regenerar: %+v %v", res2, err)
	}
	d.runAll(t)
	if len(d.rend.calls) != before+1 || d.status() != domain.CampaignDocsDone {
		t.Errorf("solo debía generarse 1: renders +%d, estado %s", len(d.rend.calls)-before, d.status())
	}
	if d.rend.calls[before].Recipient.Acudiente != "Otra Persona" {
		t.Errorf("debe usar los datos corregidos: %+v", d.rend.calls[before].Recipient)
	}
}

// ---- TemplateService ----

func TestTemplateService(t *testing.T) {
	ctx := context.Background()
	st := &fakeTpls{}
	svc := NewTemplateService(st, &fakeRender{})
	var ie *ports.InputError
	if _, err := svc.Install(ctx, "otra", []byte("BUENA")); !errors.As(err, &ie) || !strings.Contains(ie.Message, "Plantilla desconocida") {
		t.Errorf("clave desconocida: %v", err)
	}
	// Una plantilla que no pasa la validación NO reemplaza a la vigente (mensaje del cliente).
	if _, err := svc.Install(ctx, "infantil", []byte("MALA")); !errors.As(err, &ie) || ie.Message != "La plantilla no se cambió. No encontré el campo «Club: XXXX»." {
		t.Errorf("plantilla inválida: %v", err)
	}
	if len(st.put) != 0 {
		t.Error("no debe guardarse una plantilla inválida")
	}
	if b, err := svc.Install(ctx, "infantil", []byte("BUENA")); err != nil || b != "" {
		t.Errorf("primera instalación: %q %v", b, err)
	}
	if b, err := svc.Install(ctx, "infantil", []byte("BUENA2")); err != nil || !strings.HasPrefix(b, "infantil_") {
		t.Errorf("reemplazo debe devolver el respaldo: %q %v", b, err)
	}
	st.err = errors.New("disco")
	if _, err := svc.Install(ctx, "infantil", []byte("BUENA3")); err == nil || errors.As(err, &ie) {
		t.Errorf("fallo de disco = error interno: %v", err)
	}
	if _, err := svc.List(ctx); err != nil {
		t.Error(err)
	}
}

// La beca con 3 pagos recibe las 3 primeras fechas de la campaña (formato dd/mm/aaaa).
func TestFechasDelPlanSonLasNPrimerasDeLaCampania(t *testing.T) {
	d := newDocEnv(t, 25)
	cfg := d.camp.repo.camps[d.cid]
	cfg.Config.Becas[1].Pagos = []domain.Money{eurosC(70), eurosC(70), eurosC(60)} // 30 %: 3 pagos
	d.camp.repo.camps[d.cid] = cfg
	d.svc.Generate(context.Background(), d.cid)
	d.runAll(t)
	if got := strings.Join(d.rend.calls[0].FechasPago, ","); got != "01/12/2026,01/01/2027,01/02/2027" {
		t.Errorf("fechas: %s", got)
	}
}

func eurosC(n int64) domain.Money { return domain.Money(n * 100) }
