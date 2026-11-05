package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"visoria-control/internal/adapters/docx"
	"visoria-control/internal/adapters/libreoffice"
	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// Documentos de punta a punta con LibreOffice real: plantilla Word del cliente → docx rellenado en Go
// → PDF. Se omite si no hay LibreOffice, pdftotext o las plantillas del cliente (que no se versionan).
//
// En esta máquina LibreOffice viene por snap y NO puede leer /tmp: la carpeta de trabajo va dentro del
// proyecto (visible, no oculta). En un servidor con LibreOffice de apt no hay esa restricción.
func TestDocumentosConLibreOfficeReal(t *testing.T) {
	if _, err := libreoffice.Find(""); err != nil {
		t.Skip("LibreOffice no está instalado")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext no está instalado")
	}
	root := repoRoot(t)
	tplDir := filepath.Join(root, "MajesticBecas (2)", "MajesticBecas", "majestic_becas", "plantillas")
	cadete, err1 := os.ReadFile(filepath.Join(tplDir, "cadete_juvenil.docx"))
	infantil, err2 := os.ReadFile(filepath.Join(tplDir, "infantil.docx"))
	if err1 != nil || err2 != nil {
		t.Skip("no están las plantillas del cliente")
	}
	work, err := os.MkdirTemp(root, "lo-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)

	cfg := realConfig(t)
	render := docx.NewRenderer()
	mk := func(name string, tpl []byte, beca domain.BecaKey, fechas []string) string {
		rec := domain.Recipient{Jugador: name, Club: "Club de Prueba", Acudiente: "Acudiente de Prueba", Beca: beca, Anio: 2012,
			Movil: telPrueba, Correos: []string{"acudiente@example.com"}}
		out, err := render.Render(context.Background(), tpl, ports.DocumentInput{Recipient: rec, Torneo: "Copa Prueba", FechaEmision: "05/11/2026", FechasPago: fechas, Config: cfg})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := filepath.Join(work, "docs", name+".docx")
		os.MkdirAll(filepath.Dir(p), 0o750)
		if err := os.WriteFile(p, out, 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	f1 := mk("PRUEBA MAJESTIC 30", cadete, "30", []string{"01/12/2026", "01/01/2027", "01/02/2027"})
	f2 := mk("PRUEBA MAJESTIC 70", infantil, "70", []string{"01/12/2026", "01/01/2027"})

	pdfDir := filepath.Join(work, "pdf")
	if err := libreoffice.New(libreoffice.Options{WorkDir: work}).Convert(context.Background(), []string{f1, f2}, pdfDir); err != nil {
		t.Fatalf("conversión: %v", err)
	}

	// VISORIA_KEEP_PDFS=<carpeta>: conserva los PDF para revisarlos a ojo.
	if keep := os.Getenv("VISORIA_KEEP_PDFS"); keep != "" {
		os.MkdirAll(keep, 0o750)
		for _, n := range []string{"PRUEBA MAJESTIC 30", "PRUEBA MAJESTIC 70"} {
			if b, err := os.ReadFile(filepath.Join(pdfDir, n+".pdf")); err == nil {
				os.WriteFile(filepath.Join(keep, strings.ReplaceAll(n, " ", "_")+".pdf"), b, 0o640)
			}
		}
	}

	check := func(name string, plan int, want ...string) {
		out, err := exec.Command("pdftotext", "-layout", filepath.Join(pdfDir, name+".pdf"), "-").Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(out)
		for _, w := range append(want, "COPA PRUEBA", "Acudiente de Prueba", "(+57) 3209323449", "acudiente@example.com", "05/11/2026") {
			if !strings.Contains(text, w) {
				t.Errorf("%s: el PDF no contiene %q\n%s", name, w, text)
			}
		}
		if strings.Contains(text, "XXX") || strings.Contains(text, "XX/") {
			t.Errorf("%s: el PDF conserva campos sin reemplazar", name)
		}
		if n := strings.Count(text, "pago €"); n != plan {
			t.Errorf("%s: el PDF muestra %d columnas de pago, quería %d", name, n, plan)
		}
	}
	check("PRUEBA MAJESTIC 30", 3, "PRUEBA MAJESTIC 30", "€ 650", "€ 660", "01/02/2027")
	check("PRUEBA MAJESTIC 70", 2, "PRUEBA MAJESTIC 70", "€ 495", "01/01/2027")
}
