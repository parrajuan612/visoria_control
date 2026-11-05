package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

type fakeReader struct {
	rows []domain.RawRow
	err  error
}

func (f fakeReader) Read(context.Context, io.Reader) ([]domain.RawRow, error) { return f.rows, f.err }

type fakeCfg struct{ cfg *domain.MasterConfig }

func (f fakeCfg) Current() *domain.MasterConfig { return f.cfg }

func cfgFor2012() *domain.MasterConfig {
	c := validCfg()
	c.Torneos = []domain.TorneoRango{{Anios: domain.YearRange{Min: 2010, Max: 2012}, Nombre: "COPA"}}
	return &c
}

func TestImportValidaYAsignaIDs(t *testing.T) {
	rows := []domain.RawRow{
		{Fila: 2, Jugador: "Ana", Club: "C", Beca: "30%", Anio: "2012", Acudiente: "A", Movil: "3209323449"},
		{Fila: 3, Jugador: "Luis", Club: "C", Beca: "SIN BECA", Anio: "2012", Acudiente: "A", Correo: "a@b.co"},
		{Fila: 4, Jugador: "", Club: "C", Beca: "30%", Anio: "2012", Acudiente: "A", Movil: "3209323449"},
	}
	svc := NewImportService(fakeReader{rows: rows}, fakeCfg{cfgFor2012()})
	n := 0
	svc.newID = func() string { n++; return strings.Repeat("a", n) }

	res, err := svc.Import(context.Background(), strings.NewReader(""), domain.ValidationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.Total != 3 || s.Validas != 1 || s.Omitidas != 1 || s.ConErrores != 1 {
		t.Errorf("resumen: %+v", s)
	}
	if res.Recipients[0].ID != "a" || res.Recipients[2].ID != "aaa" || res.ConfigVersion == "" {
		t.Errorf("ids/version: %+v %q", res.Recipients, res.ConfigVersion)
	}
}

func TestImportErrores(t *testing.T) {
	ctx := context.Background()
	// Sin configuración sincronizada.
	if _, err := NewImportService(fakeReader{}, fakeCfg{nil}).Import(ctx, strings.NewReader(""), domain.ValidationOptions{}); !errors.Is(err, ports.ErrNoConfig) {
		t.Errorf("sin config: %v", err)
	}
	// El lector falla (formato del archivo): el error se propaga tal cual.
	bad := errors.Join(ports.ErrInvalidSpreadsheet, errors.New("faltan columnas"))
	if _, err := NewImportService(fakeReader{err: bad}, fakeCfg{cfgFor2012()}).Import(ctx, strings.NewReader(""), domain.ValidationOptions{}); !errors.Is(err, ports.ErrInvalidSpreadsheet) {
		t.Errorf("formato: %v", err)
	}
}

func TestRandomIDUnico(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := randomID()
		if len(id) != 16 || seen[id] {
			t.Fatalf("id inválido o repetido: %q", id)
		}
		seen[id] = true
	}
}
