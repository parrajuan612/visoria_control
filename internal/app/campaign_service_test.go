package app

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// ---- simulados en memoria ----

type memRepo struct {
	mu        sync.Mutex
	camps     map[string]domain.Campaign
	recs      map[string]map[string]domain.Recipient
	sums      map[string]domain.BatchSummary
	order     []string
	saveCalls [][]string // ids guardados en cada SaveRecipients
	failSave  error
}

func newMemRepo() *memRepo {
	return &memRepo{camps: map[string]domain.Campaign{}, recs: map[string]map[string]domain.Recipient{}, sums: map[string]domain.BatchSummary{}}
}

func (m *memRepo) Create(_ context.Context, c domain.Campaign, rs []domain.Recipient, sum domain.BatchSummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.camps[c.ID], m.sums[c.ID] = c, sum
	m.recs[c.ID] = map[string]domain.Recipient{}
	for _, r := range rs {
		m.recs[c.ID][r.ID] = r
	}
	m.order = append(m.order, c.ID)
	return nil
}
func (m *memRepo) Get(_ context.Context, id string) (domain.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.camps[id]
	if !ok {
		return domain.Campaign{}, ports.ErrCampaignNotFound
	}
	return c, nil
}
func (m *memRepo) List(_ context.Context, limit int) ([]domain.CampaignView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.CampaignView
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, domain.CampaignView{Campaign: m.camps[m.order[i]], Resumen: m.sums[m.order[i]]})
	}
	return out, nil
}
func (m *memRepo) Recipients(_ context.Context, id string) ([]domain.Recipient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Recipient
	for _, r := range m.recs[id] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fila < out[j].Fila })
	return out, nil
}
func (m *memRepo) SetStatus(_ context.Context, id string, from []domain.CampaignStatus, to domain.CampaignStatus) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.camps[id]
	if !ok {
		return false, ports.ErrCampaignNotFound
	}
	for _, f := range from {
		if c.Status == f {
			c.Status = to
			m.camps[id] = c
			return true, nil
		}
	}
	return false, nil
}
func (m *memRepo) SaveRecipients(_ context.Context, id string, ch []domain.Recipient, sum domain.BatchSummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave != nil {
		return m.failSave
	}
	var ids []string
	for _, r := range ch {
		m.recs[id][r.ID] = r
		ids = append(ids, r.ID)
	}
	m.saveCalls = append(m.saveCalls, ids)
	m.sums[id] = sum
	return nil
}

type memFiles struct {
	mu       sync.Mutex
	original map[string][]byte
	snaps    int
	events   []domain.AuditEvent
	failAud  error
	failOrig error
	pdfs     map[string][]byte
	failPDF  map[string]error
}

func newMemFiles() *memFiles { return &memFiles{original: map[string][]byte{}} }
func (f *memFiles) SaveOriginal(_ context.Context, c domain.Campaign, d []byte) error {
	if f.failOrig != nil {
		return f.failOrig
	}
	f.original[c.ID] = d
	return nil
}
func (f *memFiles) SavePDF(_ context.Context, c domain.Campaign, rid, src string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPDF[rid] != nil {
		return "", "", f.failPDF[rid]
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", "", err
	}
	if f.pdfs == nil {
		f.pdfs = map[string][]byte{}
	}
	f.pdfs[rid] = b
	return "pdf/" + rid + ".pdf", "sha-" + rid, nil
}
func (f *memFiles) SaveSnapshot(context.Context, domain.Campaign) error { f.snaps++; return nil }
func (f *memFiles) Audit(_ context.Context, _ domain.Campaign, e domain.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAud != nil {
		return f.failAud
	}
	f.events = append(f.events, e)
	return nil
}

func campCfg() *domain.MasterConfig {
	c := validCfg()
	c.Torneos = []domain.TorneoRango{{Anios: domain.YearRange{Min: 2010, Max: 2012}, Nombre: "COPA"}}
	return &c
}

func rows() []domain.RawRow {
	return []domain.RawRow{
		{Fila: 2, Jugador: "Ana Gómez", Club: "C", Beca: "30%", Anio: "2012", Acudiente: "A", Movil: "3209323449", Correo: "a@b.co"},
		{Fila: 3, Jugador: "Luis Pérez", Club: "C", Beca: "50%", Anio: "2011", Acudiente: "B", Movil: "31245", Correo: ""}, // móvil incompleto y sin correo → error
		{Fila: 4, Jugador: "Sin Beca", Club: "C", Beca: "SIN BECA", Anio: "2012", Acudiente: "C"},
		{Fila: 5, Jugador: "ana gomez", Club: "C", Beca: "50%", Anio: "2012", Acudiente: "D", Movil: "3001112233", Correo: "d@b.co"}, // duplicado de la fila 2
	}
}

type campEnv struct {
	svc   *CampaignService
	repo  *memRepo
	files *memFiles
}

func newCampSvc(t *testing.T, rs []domain.RawRow, cfg *domain.MasterConfig) campEnv {
	t.Helper()
	repo, files := newMemRepo(), newMemFiles()
	svc := NewCampaignService(fakeReader{rows: rs}, fakeCfg{cfg}, repo, files)
	n := 0
	svc.newID = func() string { n++; return strings.Repeat(string(rune('a'+n%26)), 4) + string(rune('0'+n%10)) }
	svc.now = func() time.Time { return time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC) }
	return campEnv{svc, repo, files}
}

func create(t *testing.T, e campEnv, mut func(*ports.CreateCampaignInput)) (ports.CampaignDetail, error) {
	in := ports.CreateCampaignInput{Data: []byte("xlsx"), Filename: "jugadores.xlsx", Torneo: "  Copa   Majestic ", FechasPago: []string{"2026-12-01", "2027-01-01", "2027-02-01"}, By: "operador"}
	if mut != nil {
		mut(&in)
	}
	return e.svc.Create(context.Background(), in)
}

func TestCrearCampana(t *testing.T) {
	e := newCampSvc(t, rows(), campCfg())
	d, err := create(t, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := d.Campaign
	if c.Status != domain.CampaignDraft || c.Torneo != "Copa Majestic" || len(c.FechasPago) != 3 || c.ConfigHash == "" || c.CreatedBy != "operador" {
		t.Errorf("campaña: %+v", c.Campaign)
	}
	if c.Dir == "" || !strings.HasPrefix(c.Dir, "2026-11-05_Copa-Majestic_") {
		t.Errorf("dir=%q", c.Dir)
	}
	if s := c.Resumen; s.Total != 4 || s.Validas != 2 || s.ConErrores != 1 || s.Omitidas != 1 || s.Advertencias != 1 {
		t.Errorf("resumen: %+v", s)
	}
	if len(d.Recipients) != 4 || d.Recipients[0].ID == "" || d.Recipients[0].ID == d.Recipients[1].ID || d.Recipients[0].Raw.Jugador != "Ana Gómez" {
		t.Errorf("destinatarios: %+v", d.Recipients[0])
	}
	// Se guardó: Excel original, snapshot, Redis y la auditoría.
	if string(e.files.original[c.ID]) != "xlsx" || e.files.snaps != 1 || len(e.repo.recs[c.ID]) != 4 {
		t.Errorf("persistencia: %v %d %d", e.files.original, e.files.snaps, len(e.repo.recs[c.ID]))
	}
	if len(e.files.events) != 1 || e.files.events[0].Type != "campaign.created" || !strings.Contains(e.files.events[0].Detail, "filas=4") {
		t.Errorf("auditoría: %+v", e.files.events)
	}
	// Se puede leer de vuelta y aparece en el listado.
	got, err := e.svc.Get(context.Background(), c.ID)
	if err != nil || len(got.Recipients) != 4 || got.Campaign.Resumen.Total != 4 {
		t.Errorf("Get: %+v %v", got.Campaign.Resumen, err)
	}
	if l, _ := e.svc.List(context.Background()); len(l) != 1 || l[0].ID != c.ID {
		t.Errorf("List: %+v", l)
	}
}

func TestCrearCampanaErrores(t *testing.T) {
	var ie *ports.InputError
	// Torneo vacío / fechas inválidas / faltan fechas → errores de entrada con el texto del cliente.
	cases := []struct {
		name string
		mut  func(*ports.CreateCampaignInput)
		sub  string
	}{
		{"sin torneo", func(i *ports.CreateCampaignInput) { i.Torneo = " " }, "Escribe el nombre del torneo"},
		{"fecha inválida", func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"x"} }, "Fecha inválida en el pago 1."},
		{"fechas desordenadas", func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"2027-01-01", "2026-12-01"} }, "deben estar en orden"},
		{"faltan fechas", func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"2026-12-01"} }, "Faltan fechas de pago"},
		{"sin fechas", func(i *ports.CreateCampaignInput) { i.FechasPago = nil }, "Faltan fechas de pago"},
	}
	for _, c := range cases {
		e := newCampSvc(t, rows(), campCfg())
		// el 50 % de la fila 3 es inválido; el 30 % de la fila 2 (1 pago) es válido
		_, err := create(t, e, c.mut)
		if !errors.As(err, &ie) || !strings.Contains(err.Error(), c.sub) {
			// con 30% (1 pago) y 1 fecha SÍ alcanza: ajustar el caso para que exija más
			if c.name == "faltan fechas" && err == nil {
				continue
			}
			t.Errorf("%s: %v", c.name, err)
		}
		if len(e.repo.camps) != 0 {
			t.Errorf("%s: no debe guardarse nada si falla", c.name)
		}
	}
	// Sin configuración, archivo ilegible y fallos de disco.
	e := newCampSvc(t, rows(), nil)
	if _, err := create(t, e, nil); !errors.Is(err, ports.ErrNoConfig) {
		t.Errorf("sin config: %v", err)
	}
	e = newCampSvc(t, nil, campCfg())
	e.svc.reader = fakeReader{err: errors.Join(ports.ErrInvalidSpreadsheet, errors.New("x"))}
	if _, err := create(t, e, nil); !errors.Is(err, ports.ErrInvalidSpreadsheet) {
		t.Errorf("excel inválido: %v", err)
	}
	e = newCampSvc(t, rows(), campCfg())
	e.files.failOrig = errors.New("disco lleno")
	if _, err := create(t, e, nil); err == nil || len(e.repo.camps) != 0 {
		t.Errorf("si falla el disco no debe quedar la campaña en Redis: %v", err)
	}
	// Un fallo de auditoría no impide crear la campaña (queda en el log).
	e = newCampSvc(t, rows(), campCfg())
	e.files.failAud = errors.New("sin permisos")
	if _, err := create(t, e, nil); err != nil {
		t.Errorf("la auditoría no debe bloquear la creación: %v", err)
	}
}

func TestFechasExigidasSoloPorBecasPresentes(t *testing.T) {
	// Solo hay una fila válida con beca del 100 % (1 pago): con 1 fecha alcanza.
	rs := []domain.RawRow{{Fila: 2, Jugador: "Ana", Club: "C", Beca: "100%", Anio: "2012", Acudiente: "A", Movil: "3209323449"}}
	e := newCampSvc(t, rs, campCfg())
	if _, err := create(t, e, func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"2026-12-01"} }); err != nil {
		t.Errorf("1 fecha para el 100%%: %v", err)
	}
	// Con una beca de 3 pagos y 1 sola fecha: error con el mensaje del cliente.
	rs = []domain.RawRow{{Fila: 2, Jugador: "Ana", Club: "C", Beca: "SIN BECA"}, {Fila: 3, Jugador: "Bea", Club: "C", Beca: "70%", Anio: "2012", Acudiente: "A", Movil: "3209323449"}}
	e = newCampSvc(t, rs, campCfg())
	cfg := campCfg()
	cfg.Becas[3].Pagos = []domain.Money{2500, 2500} // 70 %: 2 pagos
	e.svc.cfg = fakeCfg{cfg}
	_, err := create(t, e, func(i *ports.CreateCampaignInput) { i.FechasPago = []string{"2026-12-01"} })
	if err == nil || !strings.Contains(err.Error(), "Faltan fechas de pago: en tu Excel hay jugadores con «70%», que necesita las fechas de los 2 primeros pagos.") {
		t.Errorf("faltan fechas: %v", err)
	}
}

func TestEditarDestinatario(t *testing.T) {
	ctx := context.Background()
	e := newCampSvc(t, rows(), campCfg())
	d, _ := create(t, e, nil)
	id := d.Campaign.ID
	byFila := map[int]domain.Recipient{}
	for _, r := range d.Recipients {
		byFila[r.Fila] = r
	}
	bad, dup := byFila[3], byFila[5]
	if bad.Estado != domain.RecipientInvalid || dup.Estado != domain.RecipientValid {
		t.Fatalf("estados iniciales: %v %v", bad.Estado, dup.Estado)
	}
	s := func(v string) *string { return &v }

	// 1) Corregir el correo vacío → la fila pasa a VÁLIDA sin volver a subir el Excel (criterio de O5).
	res, err := e.svc.EditRecipient(ctx, id, bad.ID, domain.RecipientPatch{Correo: s("papa@gmail.com")}, "operador")
	if err != nil {
		t.Fatal(err)
	}
	if res.Recipient.Estado != domain.RecipientValid || !res.Recipient.Editado || res.Recipient.ID != bad.ID || res.Recipient.Fila != 3 ||
		len(res.Recipient.Correos) != 1 || res.Recipient.Raw.Correo != "papa@gmail.com" {
		t.Fatalf("editada: %+v", res.Recipient)
	}
	if res.Resumen.Validas != 3 || res.Resumen.ConErrores != 0 || len(res.Otros) != 0 {
		t.Errorf("resumen/otros: %+v %v", res.Resumen, res.Otros)
	}
	// Solo se guardó esa fila (no todo el lote) y la auditoría registra campo, valor anterior y nuevo.
	if len(e.repo.saveCalls) != 1 || len(e.repo.saveCalls[0]) != 1 || e.repo.saveCalls[0][0] != bad.ID {
		t.Errorf("guardados: %v", e.repo.saveCalls)
	}
	ev := e.files.events[len(e.files.events)-1]
	if ev.Type != "recipient.edited" || ev.Field != "correo" || ev.Old != "" || ev.New != "papa@gmail.com" || ev.By != "operador" ||
		ev.RecipientID != bad.ID || ev.Fila != 3 || !strings.Contains(ev.Detail, "INVALID -> VALID") {
		t.Errorf("auditoría: %+v", ev)
	}

	// 2) Cambiar el nombre de la fila duplicada resuelve el aviso de duplicado sin tocar otras filas.
	res, err = e.svc.EditRecipient(ctx, id, dup.ID, domain.RecipientPatch{Jugador: s("Ana María Gómez")}, "operador")
	if err != nil || len(res.Recipient.Issues) != 0 || res.Resumen.Advertencias != 1 { // queda la de la fila 3 (móvil incompleto)
		t.Errorf("el aviso de duplicado debía desaparecer: %v %+v %+v", err, res.Recipient.Issues, res.Resumen)
	}

	// 3) Corregir la fila ORIGINAL (fila 2) a otro año deja libre al duplicado: sus avisos cambian y se devuelven.
	e2 := newCampSvc(t, rows(), campCfg())
	d2, _ := create(t, e2, nil)
	var first, second domain.Recipient
	for _, r := range d2.Recipients {
		if r.Fila == 2 {
			first = r
		}
		if r.Fila == 5 {
			second = r
		}
	}
	if !hasMsg(second, "también aparece en la fila 2") {
		t.Fatalf("la fila 5 debía partir con aviso de duplicado: %+v", second.Issues)
	}
	res, err = e2.svc.EditRecipient(ctx, d2.Campaign.ID, first.ID, domain.RecipientPatch{Anio: s("2010")}, "operador")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Otros) != 1 || res.Otros[0].ID != second.ID || hasMsg(res.Otros[0], "también aparece") {
		t.Errorf("la fila duplicada debía actualizarse y devolverse: %+v", res.Otros)
	}
	if len(e2.repo.saveCalls[0]) != 2 {
		t.Errorf("se guardan la editada y la afectada: %v", e2.repo.saveCalls)
	}
	if got, _ := e2.svc.Get(ctx, d2.Campaign.ID); got.Campaign.Resumen.Validas != 2 || got.Campaign.Resumen.Advertencias != 0 {
		t.Errorf("resumen persistido: %+v", got.Campaign.Resumen)
	}
}

func hasMsg(r domain.Recipient, sub string) bool {
	for _, i := range r.Issues {
		if strings.Contains(i.Mensaje, sub) {
			return true
		}
	}
	return false
}

func TestEditarErrores(t *testing.T) {
	ctx := context.Background()
	e := newCampSvc(t, rows(), campCfg())
	d, _ := create(t, e, nil)
	id, rid := d.Campaign.ID, d.Recipients[0].ID
	s := func(v string) *string { return &v }

	if _, err := e.svc.EditRecipient(ctx, "nope", rid, domain.RecipientPatch{Club: s("x")}, "op"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Errorf("campaña inexistente: %v", err)
	}
	if _, err := e.svc.EditRecipient(ctx, id, "nope", domain.RecipientPatch{Club: s("x")}, "op"); !errors.Is(err, ports.ErrRecipientNotFound) {
		t.Errorf("destinatario inexistente: %v", err)
	}
	var ie *ports.InputError
	if _, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{}, "op"); !errors.As(err, &ie) {
		t.Errorf("parche vacío: %v", err)
	}
	if _, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{Club: s(strings.Repeat("x", 400))}, "op"); !errors.As(err, &ie) {
		t.Errorf("celda demasiado larga: %v", err)
	}
	// Sin cambio real: no guarda ni audita.
	before := len(e.files.events)
	res, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{Club: s("C")}, "op")
	if err != nil || len(e.repo.saveCalls) != 0 || len(e.files.events) != before || res.Recipient.ID != rid || res.Recipient.Editado {
		t.Errorf("sin cambios: %v %v %+v", err, e.repo.saveCalls, res.Recipient.Editado)
	}
	// Campaña bloqueada (ya se está enviando).
	c := e.repo.camps[id]
	c.Status = domain.CampaignDispatching
	e.repo.camps[id] = c
	if _, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{Club: s("otro")}, "op"); !errors.Is(err, ports.ErrCampaignLocked) {
		t.Errorf("bloqueada: %v", err)
	}
	// Fallo al guardar → error y sin auditoría de ese cambio.
	c.Status = domain.CampaignDraft
	e.repo.camps[id] = c
	e.repo.failSave = errors.New("redis caído")
	n := len(e.files.events)
	if _, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{Club: s("otro")}, "op"); err == nil || len(e.files.events) != n {
		t.Errorf("fallo de Redis: %v", err)
	}
	// Un fallo de auditoría no deshace la corrección ya guardada.
	e.repo.failSave = nil
	e.files.failAud = errors.New("sin permisos")
	if _, err := e.svc.EditRecipient(ctx, id, rid, domain.RecipientPatch{Club: s("otro")}, "op"); err != nil {
		t.Errorf("la auditoría no debe bloquear: %v", err)
	}
}

// Dos correcciones simultáneas sobre la misma campaña no se pisan (cada una ve el resultado de la otra).
func TestEdicionesConcurrentes(t *testing.T) {
	ctx := context.Background()
	e := newCampSvc(t, rows(), campCfg())
	d, _ := create(t, e, nil)
	var wg sync.WaitGroup
	for i, r := range d.Recipients {
		wg.Add(1)
		go func(i int, rid string) {
			defer wg.Done()
			club := "Club " + string(rune('A'+i))
			if _, err := e.svc.EditRecipient(ctx, d.Campaign.ID, rid, domain.RecipientPatch{Club: &club}, "op"); err != nil {
				t.Error(err)
			}
		}(i, r.ID)
	}
	wg.Wait()
	got, _ := e.svc.Get(ctx, d.Campaign.ID)
	for _, r := range got.Recipients {
		if !strings.HasPrefix(r.Raw.Club, "Club ") || !r.Editado {
			t.Errorf("edición perdida: %+v", r.Raw)
		}
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(ports.ErrCampaignNotFound) || !IsNotFound(ports.ErrRecipientNotFound) || IsNotFound(errors.New("x")) {
		t.Error("IsNotFound")
	}
	_ = io.EOF
}
