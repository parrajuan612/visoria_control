package domain

import (
	"strings"
	"testing"
	"time"
)

func TestParseFechasPago(t *testing.T) {
	ok := map[string][]string{
		"iso":         {"2026-12-01", "2027-01-01", "2027-02-01"},
		"dd/mm/aaaa":  {"01/12/2026", "01/01/2027"},
		"solo la 1ª":  {"2026-12-01", "", ""},
		"mezcla":      {"2026-12-01", "01/01/2027"},
		"iguales ok":  {"2026-12-01", "2026-12-01"},
		"ninguna":     {"", "", ""},
		"con espacio": {" 2026-12-01 "},
	}
	want := map[string]int{"iso": 3, "dd/mm/aaaa": 2, "solo la 1ª": 1, "mezcla": 2, "iguales ok": 2, "ninguna": 0, "con espacio": 1}
	for name, in := range ok {
		got, err := ParseFechasPago(in)
		if err != nil || len(got) != want[name] {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	if got, _ := ParseFechasPago([]string{"01/12/2026"}); got[0] != "2026-12-01" {
		t.Errorf("debe normalizar a ISO: %v", got)
	}
	// Mensajes del cliente (app.py::_fechas_por_beca).
	bad := map[string]struct {
		in  []string
		sub string
	}{
		"hueco":       {[]string{"2026-12-01", "", "2027-02-01"}, "llénalas en orden"},
		"inválida":    {[]string{"2026-12-01", "31/02/2027"}, "Fecha inválida en el pago 2."},
		"desordenada": {[]string{"2027-01-01", "2026-12-01"}, "deben estar en orden"},
		"de más":      {[]string{"2026-01-01", "2026-02-01", "2026-03-01", "2026-04-01"}, "el máximo es 3"},
		"texto":       {[]string{"mañana"}, "Fecha inválida en el pago 1."},
	}
	for name, c := range bad {
		if _, err := ParseFechasPago(c.in); err == nil || !strings.Contains(err.Error(), c.sub) {
			t.Errorf("%s: esperaba %q, fue %v", name, c.sub, err)
		}
	}
}

func TestNormalizeTorneo(t *testing.T) {
	if got, err := NormalizeTorneo("  Copa   Majestic  Internacional "); err != nil || got != "Copa Majestic Internacional" {
		t.Errorf("%q %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxTorneoLen+1)} {
		if _, err := NormalizeTorneo(bad); err == nil {
			t.Errorf("%q debía fallar", bad)
		}
	}
}

func TestCampaignDirNameYEditable(t *testing.T) {
	c := Campaign{ID: "abc123", Torneo: "COPA Majestic / Internacional ¡2026!", CreatedAt: time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)}
	if got := c.DirName(); got != "2026-11-05_COPA-Majestic-Internacional-2026_abc123" {
		t.Errorf("DirName=%q", got)
	}
	if got := (Campaign{ID: "x", Torneo: "///", CreatedAt: c.CreatedAt}).DirName(); got != "2026-11-05_torneo_x" {
		t.Errorf("sin letras: %q", got)
	}
	long := Campaign{ID: "x", Torneo: strings.Repeat("a", 100), CreatedAt: c.CreatedAt}
	if n := len([]rune(long.DirName())); n > 10+1+60+1+1 {
		t.Errorf("el nombre debe acotarse: %d", n)
	}
	// Nunca debe poder escapar de la carpeta de datos.
	if d := (Campaign{ID: "x", Torneo: "../../etc/passwd", CreatedAt: c.CreatedAt}).DirName(); strings.ContainsAny(d, `/\`) || strings.Contains(d, "..") {
		t.Errorf("DirName peligroso: %q", d)
	}
	for st, want := range map[CampaignStatus]bool{CampaignDraft: true, CampaignReady: true, CampaignDocsDone: true, CampaignDispatching: false, CampaignCompleted: false, CampaignCancelled: false, CampaignPaused: false, CampaignDocsRunning: false} {
		if (Campaign{Status: st}).Editable() != want {
			t.Errorf("Editable(%s)", st)
		}
	}
	opt := Campaign{Exigir2: true, FechasPago: []string{"2026-12-01", "2027-01-01"}}.ValidationOptions()
	if !opt.RequireBothChannels || opt.FechasPago != 2 {
		t.Errorf("opciones: %+v", opt)
	}
}

func TestCheckFechas(t *testing.T) {
	cfg := configValida()
	r30 := ValidateRow(row(2, "A", "C", "30%", "2012", "A", "3209323449", ""), cfg, ValidationOptions{})
	r70 := ValidateRow(row(3, "B", "C", "70%", "2012", "A", "3209323449", ""), cfg, ValidationOptions{})
	r100 := ValidateRow(row(4, "C", "C", "100%", "2012", "A", "3209323449", ""), cfg, ValidationOptions{})
	sb := ValidateRow(row(5, "D", "C", "SIN BECA", "2012", "A", "", ""), cfg, ValidationOptions{})
	all := []Recipient{r30, r70, r100, sb}

	if err := CheckFechas(cfg, all, 3); err != nil {
		t.Errorf("3 fechas alcanzan: %v", err)
	}
	// Mensaje del cliente.
	err := CheckFechas(cfg, all, 2)
	if err == nil || err.Error() != "Faltan fechas de pago: en tu Excel hay jugadores con «30%», que necesita las fechas de los 3 primeros pagos." {
		t.Errorf("2 fechas: %v", err)
	}
	// Solo la beca del 100 % (1 pago) basta con 1 fecha, y las filas con errores o sin beca no cuentan.
	if err := CheckFechas(cfg, []Recipient{r100, sb}, 1); err != nil {
		t.Errorf("1 fecha para el 100%%: %v", err)
	}
	if err := CheckFechas(cfg, []Recipient{r100}, 0); err == nil || !strings.Contains(err.Error(), "la fecha del 1er pago") {
		t.Errorf("0 fechas: %v", err)
	}
	bad := r30
	bad.Estado = RecipientInvalid
	if err := CheckFechas(cfg, []Recipient{bad}, 0); err != nil {
		t.Errorf("las filas inválidas no exigen fechas: %v", err)
	}
	_ = r70
}

func TestValidateRowPlanYFechas(t *testing.T) {
	cfg := configValida()
	// La campaña solo tiene 2 fechas: una beca de 3 pagos queda con error en esa fila.
	r := ValidateRow(row(2, "A", "C", "30%", "2012", "A", "3209323449", ""), cfg, ValidationOptions{FechasPago: 2})
	if r.Estado != RecipientInvalid || !issue(r, IssueError, CampoBeca, "necesita las fechas de los 3 primeros pagos y la campaña solo tiene 2") {
		t.Errorf("faltan fechas: %+v", r.Issues)
	}
	if r := ValidateRow(row(2, "A", "C", "70%", "2012", "A", "3209323449", ""), cfg, ValidationOptions{FechasPago: 2}); r.Estado != RecipientValid {
		t.Errorf("el 70%% (2 pagos) cabe en 2 fechas: %+v", r.Issues)
	}
	// Beca con propuesta sin plan de pagos (error del cliente).
	c2 := configValida()
	c2.Becas[5].Pagos = nil // ACOMPAÑANTE sin plan
	if r := ValidateRow(row(2, "A", "C", "ACOMPAÑANTE", "2012", "A", "3209323449", ""), c2, ValidationOptions{}); !issue(r, IssueError, CampoBeca, "no tiene plan de pagos configurado (ver Configuración).") {
		t.Errorf("sin plan: %+v", r.Issues)
	}
}

func TestRawRowApply(t *testing.T) {
	raw := row(2, "ANA", "Club", "50%", "2012", "María", "3209323449", "a@b.co")
	s := func(v string) *string { return &v }

	out, ch, err := raw.Apply(RecipientPatch{Movil: s(" 3001112233 "), Correo: s("")})
	if err != nil || out.Movil != "3001112233" || out.Correo != "" || out.Jugador != "ANA" || raw.Movil != "3209323449" {
		t.Fatalf("apply: %+v %v (el original no debe mutar)", out, err)
	}
	if len(ch) != 2 || ch[0] != (FieldChange{"movil", "3209323449", "3001112233"}) || ch[1] != (FieldChange{"correo", "a@b.co", ""}) {
		t.Errorf("cambios: %+v", ch)
	}
	// Sin cambio real no se reporta nada.
	if _, ch, _ := raw.Apply(RecipientPatch{Club: s("Club"), Anio: s(" 2012 ")}); len(ch) != 0 {
		t.Errorf("valores iguales no son cambios: %+v", ch)
	}
	// Todos los campos.
	all := RecipientPatch{Jugador: s("J"), Club: s("C"), Beca: s("30"), Anio: s("2010"), Acudiente: s("A"), Movil: s("1"), Correo: s("c")}
	if _, ch, _ := raw.Apply(all); len(ch) != 7 {
		t.Errorf("7 campos: %+v", ch)
	}
	if _, _, err := raw.Apply(RecipientPatch{Club: s(strings.Repeat("x", 301))}); err == nil {
		t.Error("una celda demasiado larga debe rechazarse")
	}
	if !(RecipientPatch{}).IsEmpty() || (RecipientPatch{Club: s("")}).IsEmpty() {
		t.Error("IsEmpty")
	}
}

func TestDocumentoYDespacho(t *testing.T) {
	ok := &Document{Path: "pdf/x.pdf", SHA256: "abc"}
	cases := []struct {
		name     string
		r        Recipient
		has, can bool
	}{
		{"válida con PDF", Recipient{Estado: RecipientValid, Documento: ok}, true, true},
		{"válida sin documento", Recipient{Estado: RecipientValid}, false, false},
		{"documento con error", Recipient{Estado: RecipientValid, Documento: &Document{Error: "x"}}, false, false},
		{"documento sin ruta", Recipient{Estado: RecipientValid, Documento: &Document{}}, false, false},
		{"inválida con PDF (obsoleta)", Recipient{Estado: RecipientInvalid, Documento: ok}, true, false},
		{"omitida", Recipient{Estado: RecipientOmitted, Documento: ok}, true, false},
	}
	for _, c := range cases {
		if c.r.HasDocument() != c.has || c.r.CanDispatch() != c.can {
			t.Errorf("%s: HasDocument=%v CanDispatch=%v", c.name, c.r.HasDocument(), c.r.CanDispatch())
		}
	}
	recs := []Recipient{
		{ID: "a", Estado: RecipientValid},
		{ID: "b", Estado: RecipientValid, Documento: ok},
		{ID: "c", Estado: RecipientValid, Documento: &Document{Error: "falló"}}, // ya se intentó: no es pendiente
		{ID: "d", Estado: RecipientInvalid},
		{ID: "e", Estado: RecipientOmitted},
	}
	if p := PendingDocuments(recs); len(p) != 1 || p[0].ID != "a" {
		t.Errorf("pendientes: %+v", p)
	}
}

func TestNombresDePlantillas(t *testing.T) {
	if len(TemplateKeys) != 3 || TemplateName("cadete_juvenil") != "Cadete y Juvenil" || TemplateName("benjamin_alevin") != "Benjamín y Alevín" ||
		TemplateName("infantil") != "Infantil" || TemplateName("otra") != "" {
		t.Errorf("plantillas: %v", TemplateKeys)
	}
}
