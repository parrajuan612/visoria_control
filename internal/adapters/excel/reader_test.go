package excel

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/xuri/excelize/v2"
)

// book arma un .xlsx en memoria: hojas → filas de celdas (string o número).
func book(t *testing.T, sheets map[string][][]any, order ...string) *bytes.Reader {
	t.Helper()
	f := excelize.NewFile()
	first := true
	for _, name := range order {
		if first {
			f.SetSheetName("Sheet1", name)
			first = false
		} else {
			f.NewSheet(name)
		}
		for r, row := range sheets[name] {
			for c, v := range row {
				if v == nil {
					continue
				}
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				if err := f.SetCellValue(name, cell, v); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func read(t *testing.T, r *bytes.Reader) ([]domain.RawRow, error) {
	t.Helper()
	return NewReader().Read(context.Background(), r)
}

func TestLeeColumnasEnElOrdenReal(t *testing.T) {
	// Orden real del cliente (SDD §2.1): NOMBRE, CLUB, BECA, ACUDIENTE, …, CORREO + nuevo MOVIL y año.
	rows, err := read(t, book(t, map[string][][]any{"Jugadores": {
		{"NOMBRE", "CLUB", "BECA", "AÑO NACIMIENTO", "ACUDIENTE", "MÓVIL", "CORREO"},
		{"ATOM JOSE COLMENARES MORA", "Club Uno", "50%", 2012, "María Mora", 573209323449, "mama@gmail.com"},
		{"LUIS PÉREZ", "Club Dos", 0.3, 2010, "Pedro Pérez", "320 932 3449", nil},
	}}, "Jugadores"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("filas=%d", len(rows))
	}
	want := domain.RawRow{Fila: 2, Jugador: "ATOM JOSE COLMENARES MORA", Club: "Club Uno", Beca: "50%", Anio: "2012",
		Acudiente: "María Mora", Movil: "573209323449", Correo: "mama@gmail.com"}
	if rows[0] != want {
		t.Errorf("fila 1:\n got %+v\nwant %+v", rows[0], want)
	}
	// Excel guarda 30 % como 0,3 y un móvil sin correo queda con Correo vacío.
	if rows[1].Beca != "0.3" || rows[1].Fila != 3 || rows[1].Correo != "" || rows[1].Movil != "320 932 3449" {
		t.Errorf("fila 2: %+v", rows[1])
	}
}

func TestColumnasEnOtroOrdenYConAlias(t *testing.T) {
	rows, err := read(t, book(t, map[string][][]any{"Hoja": {
		{"Listado de la visoria 2027"}, // fila de título antes de los encabezados
		{},
		{"E-mail", "Celular", "Representante", "Nacimiento", "% Beca", "Equipo", "Deportista", "COLUMNA EXTRA"},
		{"a@b.co", 3209323449, "Ana", 2011, 70, "FC", "Juan", "ignorar"},
	}}, "Hoja"))
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RawRow{Fila: 4, Jugador: "Juan", Club: "FC", Beca: "70", Anio: "2011", Acudiente: "Ana", Movil: "3209323449", Correo: "a@b.co"}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("got %+v", rows)
	}
}

func TestSoloCorreoOSoloMovil(t *testing.T) {
	for name, hdr := range map[string][]any{
		"solo móvil":  {"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "MOVIL"},
		"solo correo": {"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "CORREO"},
	} {
		row := []any{"J", "C", "50%", 2012, "A", "x"}
		if _, err := read(t, book(t, map[string][][]any{"H": {hdr, row}}, "H")); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBuscaEnLaHojaCorrecta(t *testing.T) {
	// La primera hoja es una portada; los datos están en la segunda.
	rows, err := read(t, book(t, map[string][][]any{
		"Portada":   {{"Instrucciones"}, {"Complete la hoja Jugadores"}},
		"Jugadores": {{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "CELULAR"}, {"J", "C", "50%", 2012, "A", 3209323449}},
	}, "Portada", "Jugadores"))
	if err != nil || len(rows) != 1 || rows[0].Jugador != "J" {
		t.Errorf("rows=%+v err=%v", rows, err)
	}
}

func TestSaltaFilasVacias(t *testing.T) {
	rows, err := read(t, book(t, map[string][][]any{"H": {
		{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "MOVIL"},
		{"J1", "C", "50%", 2012, "A", 3209323449},
		{nil, nil, nil, nil, nil, nil},
		{"   ", "", nil},
		{"J2", "C", "30%", 2011, "B", 3001112233},
	}}, "H"))
	if err != nil || len(rows) != 2 || rows[0].Fila != 2 || rows[1].Fila != 5 || rows[1].Jugador != "J2" {
		t.Errorf("rows=%+v err=%v", rows, err)
	}
}

func TestErroresDeFormato(t *testing.T) {
	cases := map[string]struct {
		data map[string][][]any
		sub  string
	}{
		"sin encabezados":     {map[string][][]any{"H": {{"a", "b"}, {"c", "d"}}}, "no se encontró la fila de encabezados"},
		"faltan columnas":     {map[string][][]any{"H": {{"NOMBRE", "CLUB", "BECA", "MOVIL"}, {"J", "C", "50%", 1}}}, "faltan columnas: Año de nacimiento, Acudiente"},
		"sin móvil ni correo": {map[string][][]any{"H": {{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE"}, {"J", "C", "50%", 2012, "A"}}}, "Móvil o Correo"},
		"sin filas":           {map[string][][]any{"H": {{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "MOVIL"}}}, "no tiene filas de jugadores"},
	}
	for name, c := range cases {
		_, err := read(t, book(t, c.data, "H"))
		if !errors.Is(err, ports.ErrInvalidSpreadsheet) || !strings.Contains(err.Error(), c.sub) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// No es un .xlsx
	_, err := NewReader().Read(context.Background(), strings.NewReader("esto no es un excel"))
	if !errors.Is(err, ports.ErrInvalidSpreadsheet) || !strings.Contains(err.Error(), ".xlsx") {
		t.Errorf("no es xlsx: %v", err)
	}
	// Mensaje con los encabezados esperados
	_, err = read(t, book(t, map[string][][]any{"H": {{"x", "y", "z"}}}, "H"))
	if err == nil || !strings.Contains(err.Error(), "Jugador, Club, Beca") {
		t.Errorf("mensaje de ayuda: %v", err)
	}
}

func TestTopeDeFilas(t *testing.T) {
	data := [][]any{{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "MOVIL"}}
	for i := 0; i < maxRows+1; i++ {
		data = append(data, []any{"J", "C", "50%", 2012, "A", 3209323449})
	}
	if _, err := read(t, book(t, map[string][][]any{"H": data}, "H")); err == nil || !strings.Contains(err.Error(), "máximo") {
		t.Errorf("debía rechazar por tamaño: %v", err)
	}
}

// Integra el lector con la validación: lo que lee el Excel debe pasar por las reglas del dominio.
func TestLecturaMasValidacion(t *testing.T) {
	rows, err := read(t, book(t, map[string][][]any{"H": {
		{"NOMBRE", "CLUB", "BECA", "AÑO", "ACUDIENTE", "MOVIL", "CORREO"},
		{"Prueba", "Club", "50%", 2012, "Juan Parra", 573209323449, "acudiente@example.com"},
	}}, "H"))
	if err != nil {
		t.Fatal(err)
	}
	if tel, err := domain.NormalizePhone(rows[0].Movil); err != nil || tel != "573209323449" {
		t.Errorf("el móvil numérico de Excel debe normalizarse: %q %v", tel, err)
	}
}
