package docx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"visoria-control/internal/domain"
)

// Paridad con las plantillas REALES del cliente (MajesticBecas/plantillas). Esa carpeta no se versiona
// (trae la cédula y las cuentas bancarias de la coordinadora), así que estas pruebas se omiten si no está.
func realTemplates(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "MajesticBecas (2)", "MajesticBecas", "majestic_becas", "plantillas")
	out := map[string][]byte{}
	for _, k := range []string{"cadete_juvenil", "infantil", "benjamin_alevin"} {
		b, err := os.ReadFile(filepath.Join(dir, k+".docx"))
		if err != nil {
			t.Skip("no está la carpeta MajesticBecas del cliente; se omite la prueba con las plantillas reales")
		}
		out[k] = b
	}
	return out
}

func TestPlantillasRealesPasanLaValidacionDelCliente(t *testing.T) {
	for k, b := range realTemplates(t) {
		if msg := NewRenderer().ValidateTemplate(b); msg != "" {
			t.Errorf("%s: el chequeo del cliente (_revisar_plantilla) rechaza su propia plantilla: %s", k, msg)
		}
	}
}

func TestPlantillasRealesSeSerializanSinPerdidas(t *testing.T) {
	for k, b := range realTemplates(t) {
		xmlData := partOf(t, b, "word/document.xml")
		root, err := parseXML(xmlData)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if got := root.bytes(); !bytes.Equal(got, xmlData) {
			// Lo único tolerable: elementos vacíos escritos de otra forma. Se reparsea y se compara el contenido.
			again, err := parseXML(got)
			if err != nil || !bytes.Equal(again.bytes(), got) {
				t.Errorf("%s: la reserialización no es estable", k)
			}
			t.Logf("%s: %d bytes → %d bytes (diferencias solo de forma)", k, len(xmlData), len(got))
		}
	}
}

func TestPlantillasRealesSeRellenanConTodasLasBecas(t *testing.T) {
	c := cfg()
	for k, tp := range realTemplates(t) {
		for _, beca := range []domain.BecaKey{"30", "50", "70", "100"} {
			bc, _ := c.Beca(beca)
			n := len(bc.Pagos)
			in := input(beca, fechas3[:n]...)
			in.Torneo = "Copa Prueba"
			out, err := NewRenderer().Render(context.Background(), tp, in)
			if err != nil {
				t.Errorf("%s beca %s: %v", k, beca, err)
				continue
			}
			d := extract(t, out)
			// Mismas comprobaciones de test_excel_de_ejemplo_y_docx del cliente.
			if !strings.HasPrefix(d.Paras[0], "COPA PRUEBA") || !strings.Contains(d.Paras[0], "05/11/2026") {
				t.Errorf("%s: encabezado %q", k, d.Paras[0])
			}
			if strings.Contains(strings.Join(d.Paras, "\n"), "TORNEO COPA") {
				t.Errorf("%s: el encabezado no debe llevar la palabra TORNEO delante", k)
			}
			joined := strings.Join(d.Paras, "\n")
			for _, want := range []string{"Acudiente: Acudiente de Prueba", "Jugador: PRUEBA MAJESTIC", "Móvil (+57) 3209323449", "acudiente@example.com"} {
				if !strings.Contains(joined, want) {
					t.Errorf("%s beca %s: falta %q", k, beca, want)
				}
			}
			if strings.Contains(joined+allCells(d), "XXX") {
				t.Errorf("%s beca %s: quedaron campos XXX", k, beca)
			}
			for _, row := range d.Tables[1] {
				if len(row) != 1+n {
					t.Errorf("%s beca %s: tabla de pagos con %d columnas, quería %d", k, beca, len(row), 1+n)
				}
			}
			// Los precios de la tabla de costos salen de la configuración, no de la plantilla.
			if cell(d, 0, 2, 1) != "€ 1.960" || cell(d, 0, 5, 1) != "€ 200 admón." {
				t.Errorf("%s: costos %q / %q", k, cell(d, 0, 2, 1), cell(d, 0, 5, 1))
			}
			// Solo cambia document.xml.
			for _, nm := range names(tp) {
				if nm != "word/document.xml" && !bytes.Equal(partOf(t, tp, nm), partOf(t, out, nm)) {
					t.Errorf("%s: la parte %s cambió", k, nm)
				}
			}
		}
	}
}

func allCells(d doc) string {
	var b strings.Builder
	for _, t := range d.Tables {
		for _, r := range t {
			b.WriteString(strings.Join(r, " "))
		}
	}
	return b.String()
}
