// Package integration prueba el flujo completo con las piezas reales (sin red):
// Excel → lectura por alias → validación → configuración real de Google Sheets → PDF → datos
// que se enviarían por WhatsApp. WhatsApp y Chatwoot son simulados: no se envía nada.
package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"visoria-control/internal/adapters/excel"
	"visoria-control/internal/adapters/pdfgen"
	"visoria-control/internal/adapters/sheets"
	"visoria-control/internal/app"
	"visoria-control/internal/domain"

	"github.com/xuri/excelize/v2"
)

// Móvil de prueba del proyecto (E.164 sin "+") y su forma local de 10 dígitos.
const (
	telPrueba      = "573209323449"
	telPruebaLocal = "3209323449"
)

// ---- simulados: registran lo que recibirían, no envían nada ----

type sent struct {
	Phone, Template, Language string
	Components                []interface{}
}

type fakeWA struct {
	mu   sync.Mutex
	sent []sent
}

func (f *fakeWA) SendTemplate(_ context.Context, phone, tpl, lang string, comps []interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sent{phone, tpl, lang, comps})
	return nil
}

type note struct{ Phone, Name, Content string }

type fakeChat struct{ notes []note }

func (f *fakeChat) RegisterNote(_ context.Context, phone, name, content string) error {
	f.notes = append(f.notes, note{phone, name, content})
	return nil
}

// upload simula multipart.File sobre un []byte.
type upload struct{ *bytes.Reader }

func (upload) Close() error { return nil }

// ---- utilidades ----

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		t.Fatal("no se encontró go.mod")
	}
}

// excelDePrueba arma el Excel con el formato nuevo: columna de correo y año de nacimiento solo con el año.
func excelDePrueba(t *testing.T) upload {
	t.Helper()
	f := excelize.NewFile()
	rows := [][]any{
		{"NOMBRE", "CLUB", "BECA", "AÑO NACIMIENTO", "ACUDIENTE", "MÓVIL", "CORREO"},
		{"PRUEBA MAJESTIC", "Club de Prueba", "50%", 2012, "Acudiente de Prueba", int64(573209323449), "acudiente@example.com"},
		{"JUGADOR SOLO CORREO", "Club de Prueba", "30%", 2010, "Otro Acudiente", nil, "otro@example.com"},
		{"JUGADOR SIN BECA", "Club de Prueba", "SIN BECA", 2012, "Alguien", "3001112233", nil},
		{"JUGADOR CON ERRORES", "Club de Prueba", "50%", 2020, "Alguien", "31245", nil},
	}
	for r, row := range rows {
		for c, v := range row {
			if v != nil {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				if err := f.SetCellValue("Sheet1", cell, v); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return upload{bytes.NewReader(buf.Bytes())}
}

func build(t *testing.T) (svc *app.ImportService, repo *sheets.Repository) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/adapters/sheets/testdata/informacion_torneos.csv"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, issues := sheets.Parse(bytes.NewReader(data)) // la hoja REAL del cliente
	if domain.HasErrors(issues) || domain.HasErrors(cfg.Validate()) {
		t.Fatalf("la hoja real debe ser válida: %v %v", issues, cfg.Validate())
	}
	repo = sheets.NewRepository()
	repo.Apply(cfg)
	return app.NewImportService(excel.NewReader(), repo), repo
}

func TestFlujoCompletoConElNumeroDePrueba(t *testing.T) {
	root := repoRoot(t)
	t.Chdir(root) // el generador de PDF carga logo y firma por ruta relativa
	ctx := context.Background()

	importer, repo := build(t)
	pdfs := pdfgen.New(t.TempDir())
	wa, chat := &fakeWA{}, &fakeChat{}
	svc := app.NewVisoriaService(repo, pdfs, pdfs, wa, chat, importer, app.Options{BaseURL: "https://ejemplo.test", WhatsAppPause: 0})

	// 1) Importación y validación del Excel.
	players, summary, err := svc.ProcessPlayersExcel(ctx, excelDePrueba(t))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 4 || summary.Validas != 2 || summary.Omitidas != 1 || summary.ConErrores != 1 {
		t.Fatalf("resumen: %+v", summary)
	}
	byName := map[string]domain.Player{}
	for _, p := range players {
		byName[p.Name] = p
	}
	main, onlyMail, noBeca, bad := byName["PRUEBA MAJESTIC"], byName["JUGADOR SOLO CORREO"], byName["JUGADOR SIN BECA"], byName["JUGADOR CON ERRORES"]

	if main.Status != "PENDING" || main.PrimaryPhone != telPruebaLocal || main.Email != "acudiente@example.com" ||
		main.BirthYear != 2012 || main.BirthDate != "2012" || main.Scholarship != "50%" || main.FileID == "" {
		t.Fatalf("jugador de prueba: %+v", main)
	}
	if main.Tournament.Pricing.Total != "€ 1.550,00" || main.Tournament.Pricing.Pago1 != "€ 500,00" || main.Tournament.Pricing.Pago3 != "€ 550,00" || main.Tournament.Category != "CADETE" {
		t.Errorf("precios y categoría de la hoja: %+v", main.Tournament)
	}
	if onlyMail.Status != "PENDING" || onlyMail.PrimaryPhone != "" || !hasIssue(onlyMail, domain.IssueWarning, "solo recibirá correo") {
		t.Errorf("solo correo: %+v", onlyMail)
	}
	if noBeca.Status != "OMITTED" {
		t.Errorf("sin beca: %+v", noBeca)
	}
	if bad.Status != "INVALID_MATCH" || !hasIssue(bad, domain.IssueError, "año 2020") || !hasIssue(bad, domain.IssueWarning, "MOVIL «31245» está incompleto") && !hasIssue(bad, domain.IssueError, "MOVIL «31245» está incompleto") {
		t.Errorf("con errores: %+v", bad)
	}

	// 2) Generación del PDF con los datos de la fila.
	paths, err := svc.GenerateDocuments(ctx, players)
	if err != nil || len(paths) != 2 {
		t.Fatalf("PDFs: %v %v (solo las 2 filas PENDING)", paths, err)
	}
	pdfPath := filepath.Join(pdfsDir(paths[0]), pdfs.FileName(main))
	if ok, _ := pdfs.Exists(ctx, main); !ok {
		t.Fatalf("no existe el PDF de la fila de prueba: %s", pdfPath)
	}
	text := pdfText(t, pdfPath)
	for _, want := range []string{
		"Acudiente de Prueba", "PRUEBA MAJESTIC", "Club de Prueba", "Año de Nacimiento", "2012",
		"(+57) " + telPruebaLocal,                                                                      // el prefijo +57 no se duplica
		"TORNEO COPA MIC FOOTBALL COSTA BRAVA-BARCELONA", "TORNEO IDA EASTER CUP COMUNIDAD VALENCIANA", // 2012 está en dos torneos
		"1.550", "500", "550", // precio y pagos de la beca del 50 % según la hoja
	} {
		if !strings.Contains(text, want) {
			t.Errorf("el PDF no contiene %q.\nTexto del PDF:\n%s", want, text)
		}
	}

	// 3) Datos que se enviarían por WhatsApp al número de prueba (simulado).
	progress := make(chan string, 64)
	if err := svc.DispatchWhatsAppMessages(ctx, players, progress); err != nil {
		t.Fatal(err)
	}
	close(progress)
	var msgs []string
	for m := range progress {
		msgs = append(msgs, m)
	}
	if len(wa.sent) != 1 {
		t.Fatalf("debía enviarse 1 plantilla (solo la fila con móvil y PDF): %d\n%s", len(wa.sent), strings.Join(msgs, "\n"))
	}
	got := wa.sent[0]
	if got.Phone != telPrueba || got.Template != "purchase_receipt_3" || got.Language != "es_CO" {
		t.Errorf("destino/plantilla: %+v", got)
	}
	header := got.Components[0].(map[string]interface{})["parameters"].([]interface{})[0].(map[string]interface{})["document"].(map[string]string)
	if want := "https://ejemplo.test/pdfs/" + pdfs.FileName(main); header["link"] != want {
		t.Errorf("link del PDF: %q, quería %q", header["link"], want)
	}
	body := got.Components[1].(map[string]interface{})["parameters"].([]interface{})
	wantBody := []string{"Acudiente de Prueba", "50", "PRUEBA MAJESTIC"}
	for i, w := range wantBody {
		if body[i].(map[string]string)["text"] != w {
			t.Errorf("parámetro %d del cuerpo = %q, quería %q", i+1, body[i].(map[string]string)["text"], w)
		}
	}
	if len(chat.notes) != 1 || chat.notes[0].Phone != telPrueba {
		t.Errorf("nota de Chatwoot: %+v", chat.notes)
	}
	// Los demás se omiten con el motivo correcto.
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"JUGADOR SIN BECA: SIN BECA", "JUGADOR CON ERRORES: datos inválidos", "JUGADOR SOLO CORREO: no tiene móvil"} {
		if !strings.Contains(all, want) {
			t.Errorf("falta el mensaje %q en:\n%s", want, all)
		}
	}
}

func hasIssue(p domain.Player, lvl domain.IssueLevel, sub string) bool {
	for _, i := range p.Issues {
		if i.Nivel == lvl && strings.Contains(i.Mensaje, sub) {
			return true
		}
	}
	return false
}

func pdfsDir(path string) string { return filepath.Dir(path) }

// pdfText extrae el texto del PDF con pdftotext (se omite el test si no está instalado).
func pdfText(t *testing.T, path string) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext no está instalado; se omite la verificación del texto del PDF")
	}
	out, err := exec.Command(bin, "-layout", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return string(out)
}

// realConfig es la hoja REAL del cliente (fixture de sheets).
func realConfig(t *testing.T) domain.MasterConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/adapters/sheets/testdata/informacion_torneos.csv"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, issues := sheets.Parse(bytes.NewReader(data))
	if domain.HasErrors(issues) || domain.HasErrors(cfg.Validate()) {
		t.Fatalf("la hoja real debe ser válida: %v", issues)
	}
	return cfg
}
